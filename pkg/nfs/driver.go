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
	//"k8s.io/kubernetes/pkg/util/mount"
	//"k8s.io/kubernetes/pkg/util/nsenter"
	//"k8s.io/utils/exec"

	csicommon "github.com/kubernetes-csi/drivers/pkg/csi-common"
)

type driver struct {
	csiDriver *csicommon.CSIDriver
	endpoint  string

	//ids *identityServer
	ns    *nodeServer
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

func NewNodeServer(d *driver, containerized bool) (*nodeServer, error) {
	// mounter := mount.New("")

	//if containerized {
	//	ne, err := nsenter.NewNsenter(nsenter.DefaultHostRootFsPath, exec.New())
	//	if err != nil {
	//		return nil, err
	//	}
	//	mounter = mount.NewNsenterMounter("", ne)
	//}
	glog.Infof("new node server ")

	return &nodeServer{
		DefaultNodeServer: csicommon.NewDefaultNodeServer(d.csiDriver),
		// mounter:           mounter,
	}, nil
}

func (d *driver) Run(containerized bool) {
	var err error
	s := csicommon.NewNonBlockingGRPCServer()

	d.ns, err = NewNodeServer(d, containerized)
	if err != nil {
		glog.Infof("failed to start node server, err %v\n", err)
		os.Exit(-1)
	}

	s.Start(d.endpoint,
		csicommon.NewDefaultIdentityServer(d.csiDriver),
		getControllerServer(d.csiDriver),
		d.ns)
	s.Wait()
}
