/*
Copyright 2017 The Kubernetes Authors.

Licensed under the Apache License, Version 2.0 (the "License");
you may not use this file except in compliance with the License.
You may obtain a copy of the License at

    http://www.apache.org/licenses/LICENSE-2.0

Unless required by applicable law or agreed to in writing, software
distributed under the License is distributed on an "AS IS" BASIS,
WITHOUT WARRANTIES OR CONDITIONS OF ANY KIND, either express or implied.
See the License for the specific language governing permissions and
limitations under the License.
*/

package nfs

import (
	"os"

	"github.com/container-storage-interface/spec/lib/go/csi"
	"github.com/golang/glog"

	csicommon "github.com/kubernetes-csi/drivers/pkg/csi-common"
)

type driver struct {
	csiDriver *csicommon.CSIDriver
	endpoint  string

	ids   *csicommon.DefaultIdentityServer
	ns    *nodeServer
	cs    *ControllerServer
	cap   []*csi.VolumeCapability_AccessMode
	cscap []*csi.ControllerServiceCapability
}

const (
	driverName = "csi-nfsplugin"
)

var (
	version = "1.0.0"
)

func NewDriver(nodeID, endpoint string) *driver {
	glog.Infof("Driver: %v version: %v", driverName, version)

	d := &driver{}

	d.endpoint = endpoint

	csiDriver := csicommon.NewCSIDriver(driverName, version, nodeID)
	csiDriver.AddVolumeCapabilityAccessModes([]csi.VolumeCapability_AccessMode_Mode{
		csi.VolumeCapability_AccessMode_MULTI_NODE_MULTI_WRITER,
	})

	csiDriver.AddControllerServiceCapabilities([]csi.ControllerServiceCapability_RPC_Type{
		csi.ControllerServiceCapability_RPC_CREATE_DELETE_VOLUME,
		csi.ControllerServiceCapability_RPC_PUBLISH_UNPUBLISH_VOLUME,
		csi.ControllerServiceCapability_RPC_LIST_VOLUMES,
	})

	d.csiDriver = csiDriver

	return d
}

func NewNodeServer(d *driver) (*nodeServer, error) {
	return &nodeServer{
		DefaultNodeServer: csicommon.NewDefaultNodeServer(d.csiDriver),
	}, nil
}

func (d *driver) Run() {
	var err error
	s := csicommon.NewNonBlockingGRPCServer()

	d.ns, err = NewNodeServer(d)
	if err != nil {
		glog.Infof("failed to start node server, err %v\n", err)
		os.Exit(-1)
	}

	d.ids = csicommon.NewDefaultIdentityServer(d.csiDriver)
	d.cs = getControllerServer(d.csiDriver)

	s.Start(d.endpoint, d.ids, d.cs, d.ns)
	s.Wait()
}
