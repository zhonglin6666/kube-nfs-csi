package nfs

import (
	"os"
	"path/filepath"

	"github.com/container-storage-interface/spec/lib/go/csi"
	"github.com/golang/glog"
	"github.com/kubernetes-csi/csi-lib-utils/protosanitizer"
	"github.com/kubernetes-csi/drivers/pkg/csi-common"
	"github.com/pborman/uuid"
	"github.com/pkg/errors"
	"golang.org/x/net/context"
	"google.golang.org/grpc/codes"
	"google.golang.org/grpc/status"

	"github.com/zhonglin6666/kube-nfs-csi/pkg/util"
)

const (
	oneGB = 1073741824

	mountPath = "/persistentvolumes"
)

type ControllerServer struct {
	*csicommon.DefaultControllerServer
}

type nfsVolume struct {
	VolName            string `json:"volName"`
	VolID              string `json:"volID"`
	Server             string `json:"server"`
	Path               string `json:"path"`
	Provisioner        string `json:"provisioner"`
	VolSize            int64  `json:"volSize"`
	AdminID            string `json:"adminId"`
	UserID             string `json:"userId"`
	Mounter            string `json:"mounter"`
	DisableInUseChecks bool   `json:"disableInUseChecks"`
	ClusterID          string `json:"clusterId"`
}

func (cs ControllerServer) ControllerExpandVolume(ctx context.Context, req *csi.ControllerExpandVolumeRequest) (*csi.ControllerExpandVolumeResponse, error) {
	return nil, status.Error(codes.Unimplemented, "")
}

func getControllerServer(csiDriver *csicommon.CSIDriver) ControllerServer {
	return ControllerServer{
		csicommon.NewDefaultControllerServer(csiDriver),
	}
}

func (cs ControllerServer) validateVolumeReq(req *csi.CreateVolumeRequest) error {
	if err := cs.Driver.ValidateControllerServiceRequest(csi.ControllerServiceCapability_RPC_CREATE_DELETE_VOLUME); err != nil {
		glog.Infof("invalid create volume req: %v", protosanitizer.StripSecrets(req))
		return err
	}
	// Check sanity of request Name, Volume Capabilities
	if len(req.Name) == 0 {
		return status.Error(codes.InvalidArgument, "Volume Name cannot be empty")
	}
	if req.VolumeCapabilities == nil {
		return status.Error(codes.InvalidArgument, "Volume Capabilities cannot be empty")
	}
	return nil
}

func (cs ControllerServer) CreateVolume(ctx context.Context, req *csi.CreateVolumeRequest) (*csi.CreateVolumeResponse, error) {
	glog.Infof("controller server create volume begin req: %v", req)
	if err := cs.validateVolumeReq(req); err != nil {
		return nil, err
	}

	glog.Infof("Create volume pass to validateVolumeReq 11111")

	util.VolumeNameMutex.LockKey(req.GetName())
	defer func() {
		if err := util.VolumeNameMutex.UnlockKey(req.GetName()); err != nil {
			glog.Warningf("failed to unlock mutex volume:%s %v", req.GetName(), err)
		}
	}()

	//// Need to check for already existing volume name, and if found
	//// check for the requested capacity and already allocated capacity
	//if exVol, err := getRBDVolumeByName(req.GetName()); err == nil {
	//	// Since err is nil, it means the volume with the same name already exists
	//	// need to check if the size of existing volume is the same as in new
	//	// request
	//	if exVol.VolSize >= req.GetCapacityRange().GetRequiredBytes() {
	//		// existing volume is compatible with new request and should be reused.
	//
	//		if err = storeVolumeMetadata(exVol, cs.MetadataStore); err != nil {
	//			return nil, status.Error(codes.Internal, err.Error())
	//		}
	//
	//		// TODO (sbezverk) Do I need to make sure that RBD volume still exists?
	//		return &csi.CreateVolumeResponse{
	//			Volume: &csi.Volume{
	//				VolumeId:      exVol.VolID,
	//				CapacityBytes: exVol.VolSize,
	//				VolumeContext: req.GetParameters(),
	//			},
	//		}, nil
	//	}
	//	return nil, status.Errorf(codes.AlreadyExists, "Volume with the same name: %s but with different size already exist", req.GetName())
	//}

	glog.Infof("Create volume before to parseVolCreateRequest")
	nfsVol, err := parseVolCreateRequest(req)
	if err != nil {
		return nil, err
	}
	glog.Infof("Create volume pass to parseVolCreateRequest")

	// Check if there is already RBD image with requested name
	err = cs.checkRBDStatus(nfsVol, req, int(nfsVol.VolSize))
	if err != nil {
		return nil, err
	}
	// store volume size in  bytes (snapshot and check existing volume needs volume
	// size in bytes)
	//rbdVol.VolSize = rbdVol.VolSize * util.MiB

	//pvName := strings.Join([]string{nfsVol.VolName}, "-")
	fullPath := filepath.Join(mountPath, nfsVol.VolID)
	if err := os.MkdirAll(fullPath, 0777); err != nil {
		return nil, errors.New("unable to create directory to provision new pv: " + err.Error())
	}
	os.Chmod(fullPath, 0777)

	// path := filepath.Join(nfsVol.Path, pvName)

	return &csi.CreateVolumeResponse{
		Volume: &csi.Volume{
			VolumeId: nfsVol.VolID,
		},
	}, nil
}

func parseVolCreateRequest(req *csi.CreateVolumeRequest) (*nfsVolume, error) {
	isMultiNode := false

	for _, cap := range req.VolumeCapabilities {
		// RO modes need to be handled indepedently (ie right now even if access mode is RO, they'll be RW upon attach)
		if cap.GetAccessMode().GetMode() == csi.VolumeCapability_AccessMode_MULTI_NODE_MULTI_WRITER {
			isMultiNode = true
		}
	}

	// We want to fail early if the user is trying to create a RWX on a non-block type device
	if isMultiNode {
		return nil, status.Error(codes.InvalidArgument, "multi node access modes are only supported on rbd `block` type volumes")
	}

	// if it's NOT SINGLE_NODE_WRITER and it's BLOCK we'll set the parameter to ignore the in-use checks
	nfsVol, err := getnfsVolumeOptions(req.GetParameters(), true)
	if err != nil {
		return nil, status.Error(codes.InvalidArgument, err.Error())
	}

	// Generating Volume Name and Volume ID, as according to CSI spec they MUST be different
	volName := req.GetName()
	uniqueID := uuid.NewUUID().String()
	nfsVol.VolName = volName
	volumeID := "csi-nfs-vol-" + uniqueID
	nfsVol.VolID = volumeID
	// Volume Size - Default is 1 GiB
	//volSizeBytes := int64(oneGB)
	//if req.GetCapacityRange() != nil {
	//	volSizeBytes = req.GetCapacityRange().GetRequiredBytes()
	//}

	//nfsVol.VolSize = volSizeBytes

	return nfsVol, nil
}

func getnfsVolumeOptions(volOptions map[string]string, disableInUseChecks bool) (*nfsVolume, error) {
	glog.Infof("zzlin getnfsVolumeOptions volOptioins: %v", volOptions)
	var (
		ok bool
	)

	nfsVol := &nfsVolume{}
	nfsVol.Server, ok = volOptions["server"]
	if !ok {
		return nil, errors.New("missing required parameter pool")
	}

	nfsVol.Path, ok = volOptions["path"]
	if !ok {
		return nil, errors.New("missing required parameter path")
	}

	return nfsVol, nil
}

func (cs *ControllerServer) checkRBDStatus(nfsVol *nfsVolume, req *csi.CreateVolumeRequest, volSizeMiB int) error {
	// TODO

	return nil
}

// DeleteVolume deletes the volume in backend and removes the volume metadata
// from store
func (cs ControllerServer) DeleteVolume(ctx context.Context, req *csi.DeleteVolumeRequest) (*csi.DeleteVolumeResponse, error) {
	if err := cs.Driver.ValidateControllerServiceRequest(csi.ControllerServiceCapability_RPC_CREATE_DELETE_VOLUME); err != nil {
		glog.Warningf("invalid delete volume req: %v", protosanitizer.StripSecrets(req))
		return nil, err
	}
	// For now the image get unconditionally deleted, but here retention policy can be checked
	volumeID := req.GetVolumeId()
	util.VolumeNameMutex.LockKey(volumeID)

	defer func() {
		if err := util.VolumeNameMutex.UnlockKey(volumeID); err != nil {
			glog.Warningf("failed to unlock mutex volume:%s %v", volumeID, err)
		}
	}()

	nfsVol := &nfsVolume{}
	//if err := cs.MetadataStore.Get(volumeID, nfsVol); err != nil {
	//	if err, ok := err.(*util.CacheEntryNotFound); ok {
	//		glog.V(3).Infof("metadata for volume %s not found, assuming the volume to be already deleted (%v)", volumeID, err)
	//		return &csi.DeleteVolumeResponse{}, nil
	//	}
	//
	//	return nil, err
	//}

	volName := nfsVol.VolName
	// Deleting rbd image

	// pvName := strings.Join([]string{nfsVol.VolName}, "-")
	fullPath := filepath.Join(mountPath, volumeID)
	glog.Infof("deleting volume %s path: %v", volName, fullPath)

	if _, err := os.Stat(fullPath); os.IsNotExist(err) {
		glog.Warningf("path %s does not exist, deletion skipped", fullPath)
		return nil, nil
	}

	if err := os.RemoveAll(fullPath); err != nil {
		glog.Error("nfs volume can not remove path: %v", fullPath)
	}

	return &csi.DeleteVolumeResponse{}, nil
}
