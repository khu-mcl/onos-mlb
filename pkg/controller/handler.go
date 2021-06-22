// SPDX-FileCopyrightText: 2020-present Open Networking Foundation <info@opennetworking.org>
//
// SPDX-License-Identifier: LicenseRef-ONF-Member-1.0

package controller

import (
	"context"
	"github.com/atomix/go-client/pkg/client/errors"
	"github.com/onosproject/onos-lib-go/pkg/logging"
	"github.com/onosproject/onos-mlb/pkg/monitor"
	"github.com/onosproject/onos-mlb/pkg/southbound/e2control"
	"github.com/onosproject/onos-mlb/pkg/store/storage"
	meastype "github.com/onosproject/rrm-son-lib/pkg/model/measurement/type"
	"time"
)

var log = logging.GetLogger("controller")

const (
	RcPreRanParamDefaultOCN = meastype.QOffset0dB
	OCNDeltaFactor = 3
)

func NewHandler(interval int, e2controlHandler e2control.Handler,
	monitorHandler monitor.Handler,
	numUEsMeasStore storage.Store,
	neighborMeasStore storage.Store,
	statisticsStore storage.Store,
	ocnStore storage.Store) Handler {
	return &handler{
		e2controlHandler: e2controlHandler,
		monitorHandler: monitorHandler,
		numUEsMeasStore: numUEsMeasStore,
		neighborMeasStore: neighborMeasStore,
		statisticsStore: statisticsStore,
		ocnStore: ocnStore,
	}
}

type Handler interface {

}

type handler struct {
	e2controlHandler e2control.Handler
	monitorHandler monitor.Handler
	numUEsMeasStore storage.Store
	neighborMeasStore storage.Store
	statisticsStore storage.Store
	ocnStore storage.Store

	interval int
	overloadThreshold int
	targetThreshold int
}

func (h *handler) Run(ctx context.Context) error {
	for {
		select {
		case <- time.After(time.Duration(h.interval) * time.Second):
			// ToDo should run as goroutine
			h.StartControlLogic(ctx)
		case <- ctx.Done():
			return nil
		}
	}
}

func (h *handler) StartControlLogic(ctx context.Context) {
	// run monitor handler
	err := h.monitorHandler.Monitor(ctx)
	if err != nil {
		log.Error(err)
		return
	}

	// update ocn store - to update neighbor or to add new cells coming
	err = h.updateOcnStore(ctx)
	if err != nil {
		log.Error(err)
		return
	}

	// Get total num UE
	totalNumUEs, err := h.getTotalNumUEs(ctx)
	if err != nil {
		log.Error(err)
		return
	}

	// Get Cell IDs
	cells, err := h.getCellList(ctx)
	if err != nil {
		log.Error(err)
		return
	}

	// run control logic for each cell
	for _, cell := range cells {
		err = h.controlLogicEachCell(ctx, cell, cells, totalNumUEs)
		if err != nil {
			log.Error(err)
			return
		}
	}
}

func (h *handler) updateOcnStore(ctx context.Context) error {
	ch := make(chan *storage.Entry)
	err := h.neighborMeasStore.ListElements(ctx, ch)
	if err != nil {
		return err
	}

	for e := range ch {
		ids := e.Key
		neighborList := e.Value.(storage.Neighbors).Value

		if e, err := h.ocnStore.Get(ctx, ids); err != nil {
			// the new cells connected
			nOcnMap := make(map[storage.IDs]meastype.QOffsetRange)
			for _, nIDs := range neighborList {
				nOcnMap[nIDs] = RcPreRanParamDefaultOCN
			}
			_, err = h.ocnStore.Put(ctx, ids, storage.OcnMap{
				Value: nOcnMap,
			})
			if err != nil {
				return err
			}
		} else {
			nOcnMap := e.Value.(storage.OcnMap).Value
			// delete removed neighbor
			for k := range nOcnMap {
				if !h.containsIDs(k, neighborList) {
					delete(nOcnMap, k)
				}
			}
			// add new neighbor
			for _, n := range neighborList {
				if _, ok := nOcnMap[n]; !ok {
					nOcnMap[n] = RcPreRanParamDefaultOCN
				}
			}
		}
	}

	return nil
}

func (h *handler) containsIDs(ids storage.IDs, idsList []storage.IDs) bool {
	for _, e := range idsList {
		if e == ids {
			return true
		}
	}
	return false
}

func (h *handler) getTotalNumUEs(ctx context.Context) (int, error) {
	result := 0
	ch := make(chan *storage.Entry)
	err := h.numUEsMeasStore.ListElements(ctx, ch)
	if err != nil {
		return result, err
	}
	for e := range ch {
		result += e.Value.(int)
	}
	return result, nil
}

func (h *handler) getCellList(ctx context.Context) ([]storage.IDs, error) {
	result := make([]storage.IDs, 0)
	ch := make(chan storage.IDs)
	err := h.numUEsMeasStore.ListKeys(ctx, ch)
	if err != nil {
		return result, err
	}
	for k := range ch {
		result = append(result, k)
	}
	return result, nil
}

func (h *handler) controlLogicEachCell(ctx context.Context, ids storage.IDs, cells []storage.IDs, totalNumUEs int) error {
	neighbors, err := h.neighborMeasStore.Get(ctx, ids)
	if err != nil {
		return err
	}

	// calculate for each capacity and check sCell's and its neighbors' capacity
	// if sCell load < target load threshold
	// reduce Ocn
	neighborList := neighbors.Value.([]storage.IDs)
	numUEsSCell, err := h.numUE(ctx, ids.PlmnID, ids.CellID, cells)
	if err != nil {
		return err
	}
	capSCell := h.getCapacity(1, totalNumUEs, numUEsSCell)
	if 1 - capSCell < h.targetThreshold {
		// send control message to reduce OCn for all neighbors
		for _, nCellID := range neighborList {
			entry, err := h.ocnStore.Get(ctx, ids)
			if err != nil {
				return err
			}
			ocn := entry.Value.(storage.OcnMap).Value[nCellID]
			if ocn - OCNDeltaFactor < meastype.QOffsetMinus24dB {
				ocn = meastype.QOffsetMinus24dB
			} else {
				ocn = ocn - OCNDeltaFactor
			}

			h.e2controlHandler.SendControlMessage(ctx, nCellID, ids.NodeID, int32(ocn))
		}
	}

	// if sCell load > overload threshold && nCell < target load threshold
	// increase Ocn
	if 1 - capSCell > h.overloadThreshold {
		for _, nCellID := range neighborList {
			numUEsNCell, err := h.numUE(ctx, nCellID.PlmnID, nCellID.CellID, cells)
			if err != nil {
				log.Warnf("there is no num(UEs) measurement value; this neighbor (plmnid-%v:cid-%v) may not be controlled by this xAPP; set num(UEs) to 0", nCellID.PlmnID, nCellID.CellID)
			}
			capNCell := h.getCapacity(1, totalNumUEs, numUEsNCell)
			if 1 - capNCell < h.targetThreshold {
				entry, err := h.ocnStore.Get(ctx, ids)
				if err != nil {
					return err
				}
				ocn := entry.Value.(storage.OcnMap).Value[nCellID]
				if ocn + OCNDeltaFactor > meastype.QOffsetMinus24dB {
					ocn = meastype.QOffset24dB
				} else {
					ocn = ocn + OCNDeltaFactor
				}
				h.e2controlHandler.SendControlMessage(ctx, nCellID, ids.NodeID, int32(ocn))
			}
		}
	}

	return nil
}

func (h *handler) getCapacity(denominationFactor float64, totalNumUEs int, numUEs int) int {
	capacity := (1 - float64(numUEs) / (denominationFactor * float64(totalNumUEs))) * 100
	return int(capacity)
}

func (h *handler) numUE(ctx context.Context, plmnID string, cid string, cells []storage.IDs) (int, error) {
	storageID, err := h.findIDWithCGI(plmnID, cid, cells)
	if err != nil {
		return 0, err
	}

	entry, err := h.numUEsMeasStore.Get(ctx, storageID)
	if err != nil {
		return 0, err
	}
	return entry.Value.(int), nil
}

func (h *handler) findIDWithCGI(plmnid string, cid string, cells []storage.IDs) (storage.IDs, error) {
	for _, cell := range cells {
		if cell.PlmnID == plmnid && cell.CellID == cid {
			return cell, nil
		}
	}
	return storage.IDs{}, errors.NewNotFound("ID not found with plmnid and cgi")
}