package nfs

import (
	"fmt"
	"os"
	"path/filepath"

	"github.com/container-storage-interface/spec/lib/go/csi"
	"github.com/golang/glog"
	"github.com/kubernetes-csi/csi-lib-utils/protosanitizer"
	"github.com/kubernetes-csi/drivers/pkg/csi-common"
	"github.com/pborman/uuid"
	"github.com/pkg/errors"
	"github.com/zhonglin6666/kube-nfs-csi/pkg/util"
	"golang.org/x/net/context"
	"google.golang.org/grpc/codes"
	"google.golang.org/grpc/status"
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
	Share              string `json:"share"`
	Provisioner        string `json:"provisioner"`
	VolSize            int64  `json:"volSize"`
	AdminID            string `json:"adminId"`
	UserID             string `json:"userId"`
	Mounter            string `json:"mounter"`
	DisableInUseChecks bool   `json:"disableInUseChecks"`
	ClusterID          string `json:"clusterId"`
}

func (cs *ControllerServer) ControllerExpandVolume(ctx context.Context, req *csi.ControllerExpandVolumeRequest) (*csi.ControllerExpandVolumeResponse, error) {
	return nil, status.Error(codes.Unimplemented, "")
}

func NewControllerServer(csiDriver *csicommon.CSIDriver) *ControllerServer {
	return &ControllerServer{
		csicommon.NewDefaultControllerServer(csiDriver),
	}
}

func (cs *ControllerServer) validateVolumeReq(req *csi.CreateVolumeRequest) error {
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

func (cs *ControllerServer) CreateVolume(ctx context.Context, req *csi.CreateVolumeRequest) (*csi.CreateVolumeResponse, error) {
	glog.Infof("controller server create volume begin request: %v", req)
	if err := cs.validateVolumeReq(req); err != nil {
		return nil, err
	}

	util.VolumeNameMutex.LockKey(req.GetName())
	defer func() {
		if err := util.VolumeNameMutex.UnlockKey(req.GetName()); err != nil {
			glog.Warningf("failed to unlock mutex volume:%s %v", req.GetName(), err)
		}
	}()

	nfsVol, err := parseVolCreateRequest(req)
	if err != nil {
		return nil, err
	}

	// Check if there is already nfs with requested name
	err = cs.checkNfsStatus(nfsVol, req, int(nfsVol.VolSize))
	if err != nil {
		return nil, err
	}

	//mounter := mount.New("")
	//nfsPath := fmt.Sprintf("%v:%v", nfsVol.Server, nfsVol.Share)
	//err = mounter.Mount(nfsPath, mountPath, "nfs", nil)
	//if err != nil {
	//	if os.IsPermission(err) {
	//		return nil, status.Error(codes.PermissionDenied, err.Error())
	//	}
	//	if strings.Contains(err.Error(), "invalid argument") {
	//		return nil, status.Error(codes.InvalidArgument, err.Error())
	//	}
	//	return nil, status.Error(codes.Internal, err.Error())
	//}
	//glog.Infof("mount nfs %v success", nfsPath)
	//
	//defer func() {
	//	err = mount.CleanupMountPoint(mountPath, mounter, false)
	//	if err != nil {
	//		glog.Errorf("umount %v error: %v", mountPath, err)
	//	} else {
	//		glog.Infof("umount nfs: %v success", nfsPath)
	//	}
	//}()

	fullPath := filepath.Join(mountPath, nfsVol.VolID)
	if err := os.MkdirAll(fullPath, 0777); err != nil {
		return nil, errors.New("unable to create directory to provision new pv: " + err.Error())
	}
	if err := os.Chmod(fullPath, 0777); err != nil {
		return nil, status.Error(codes.Internal, err.Error())
	}
	glog.Infof("create volume path: %v", fullPath)

	volumeContext := req.GetParameters()
	if _, ok := volumeContext["share"]; ok {
		volumeContext["share"] = fmt.Sprintf("%s/%s", nfsVol.Share, nfsVol.VolID)
	}
	glog.Infof("create volume success, path: %v", fullPath)

	return &csi.CreateVolumeResponse{
		Volume: &csi.Volume{
			VolumeId:      nfsVol.VolID,
			CapacityBytes: nfsVol.VolSize,
			VolumeContext: req.GetParameters(),
		},
	}, nil
}

func parseVolCreateRequest(req *csi.CreateVolumeRequest) (*nfsVolume, error) {
	nfsVol, err := getnfsVolumeOptions(req.GetParameters(), true)
	if err != nil {
		return nil, status.Error(codes.InvalidArgument, err.Error())
	}

	// Generating Volume Name and Volume ID, as according to CSI spec they MUST be different
	nfsVol.VolName = req.GetName()
	volumeID := "csi-nfs-vol-" + uuid.NewUUID().String()
	nfsVol.VolID = volumeID
	// Volume Size - Default is 1 GiB
	volSizeBytes := int64(oneGB)
	if req.GetCapacityRange() != nil {
		volSizeBytes = req.GetCapacityRange().GetRequiredBytes()
	}

	nfsVol.VolSize = volSizeBytes

	return nfsVol, nil
}

func getnfsVolumeOptions(volOptions map[string]string, disableInUseChecks bool) (*nfsVolume, error) {
	var (
		ok bool
	)

	nfsVol := &nfsVolume{}
	nfsVol.Server, ok = volOptions["server"]
	if !ok {
		return nil, errors.New("missing required parameter pool")
	}

	nfsVol.Share, ok = volOptions["share"]
	if !ok {
		return nil, errors.New("missing required parameter share")
	}

	return nfsVol, nil
}

func (cs *ControllerServer) checkNfsStatus(nfsVol *nfsVolume, req *csi.CreateVolumeRequest, volSizeMiB int) error {
	// TODO
	return nil
}

// DeleteVolume deletes the volume in backend
func (cs *ControllerServer) DeleteVolume(ctx context.Context, req *csi.DeleteVolumeRequest) (*csi.DeleteVolumeResponse, error) {
	if err := cs.Driver.ValidateControllerServiceRequest(csi.ControllerServiceCapability_RPC_CREATE_DELETE_VOLUME); err != nil {
		glog.Warningf("invalid delete volume req: %v", protosanitizer.StripSecrets(req))
		return nil, err
	}
	// For now the image get unconditionally deleted, but here retention policy can be checked
	volumeID := req.GetVolumeId()
	util.VolumeNameMutex.LockKey(volumeID)

	if volumeID == "" {
		return nil, errors.New("volume id is nil")
	}

	defer func() {
		if err := util.VolumeNameMutex.UnlockKey(volumeID); err != nil {
			glog.Warningf("failed to unlock mutex volume:%s %v", volumeID, err)
		}
	}()

	nfsVol := &nfsVolume{}
	volName := nfsVol.VolName
	fullPath := filepath.Join(mountPath, volumeID)
	glog.Infof("deleting volume %s path: %v", volName, fullPath)

	//mounter := mount.New("")
	//err := mounter.Mount(fmt.Sprintf("%v:%v", nfsVol.Server, nfsVol.Share), mountPath, "nfs", nil)
	//if err != nil {
	//	if os.IsPermission(err) {
	//		return nil, status.Error(codes.PermissionDenied, err.Error())
	//	}
	//	if strings.Contains(err.Error(), "invalid argument") {
	//		return nil, status.Error(codes.InvalidArgument, err.Error())
	//	}
	//	return nil, status.Error(codes.Internal, err.Error())
	//}
	//
	//defer func() {
	//	err = mount.CleanupMountPoint(mountPath, mounter, false)
	//	if err != nil {
	//		glog.Errorf("umount %v error: %v", mountPath, err)
	//	}
	//}()

	if _, err := os.Stat(fullPath); os.IsNotExist(err) {
		glog.Warningf("path %s does not exist, deletion skipped", fullPath)
		return nil, nil
	}

	if err := os.RemoveAll(fullPath); err != nil {
		glog.Error("nfs volume can not remove path: %v", fullPath)
	}

	return &csi.DeleteVolumeResponse{}, nil
}

func (cs *ControllerServer) ControllerPublishVolume(ctx context.Context, req *csi.ControllerPublishVolumeRequest) (*csi.ControllerPublishVolumeResponse, error) {
	return &csi.ControllerPublishVolumeResponse{}, nil
}

func (cs *ControllerServer) ControllerUnpublishVolume(ctx context.Context, req *csi.ControllerUnpublishVolumeRequest) (*csi.ControllerUnpublishVolumeResponse, error) {
	return &csi.ControllerUnpublishVolumeResponse{}, nil
}
