// SPDX-FileCopyrightText: 2020-present Open Networking Foundation <info@opennetworking.org>
//
// SPDX-License-Identifier: LicenseRef-ONF-Member-1.0

package overload

import (
	"github.com/onosproject/helmit/pkg/helm"
	"github.com/onosproject/helmit/pkg/input"
	"github.com/onosproject/helmit/pkg/test"
	"github.com/onosproject/onos-mlb/test/utils"
)

// TestSuite has sdran helm release variable and test suite
type TestSuite struct {
	sdran *helm.HelmRelease
	test.Suite
}

// SetupTestSuite prepares test setup
func (s *TestSuite) SetupTestSuite(c *input.Context) error {
	// write files
	err := utils.WriteFile("/tmp/tls.cacrt", utils.TLSCacrt)
	if err != nil {
		return err
	}
	err = utils.WriteFile("/tmp/tls.crt", utils.TLSCrt)
	if err != nil {
		return err
	}
	err = utils.WriteFile("/tmp/tls.key", utils.TLSKey)
	if err != nil {
		return err
	}
	err = utils.WriteFile("/tmp/config.json", utils.ConfigJSON)
	if err != nil {
		return err
	}

	sdran, err := utils.CreateSdranRelease(c)
	if err != nil {
		return err
	}
	s.sdran = sdran
	sdran.Set("ran-simulator.pci.metricName", "three-cell-n-node-metrics").
		Set("ran-simulator.pci.modelName", "three-cell-n-node-model")
	return sdran.Install(true)
}
