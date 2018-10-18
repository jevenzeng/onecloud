package azure

import (
	"fmt"
	"strings"
	"time"

	"yunion.io/x/jsonutils"
	"yunion.io/x/log"
	"yunion.io/x/onecloud/pkg/cloudprovider"
	"yunion.io/x/onecloud/pkg/compute/models"
)

type StorageAccountTypes string

const (
	// StorageAccountTypesPremiumLRS ...
	StorageAccountTypesPremiumLRS StorageAccountTypes = "Premium_LRS"
	// StorageAccountTypesStandardLRS ...
	StorageAccountTypesStandardLRS StorageAccountTypes = "Standard_LRS"
	// StorageAccountTypesStandardSSDLRS ...
	StorageAccountTypesStandardSSDLRS StorageAccountTypes = "StandardSSD_LRS"
)

type DiskSku struct {
	Name string `json:"name"`
	Tier string
}

type ImageDiskReference struct {
	ID  string
	Lun int32 `json:"lun,omitempty"`
}

type CreationData struct {
	CreateOption     string `json:"createOption"`
	StorageAccountID string
	ImageReference   *ImageDiskReference `json:"imageReference,omitempty"`
	SourceURI        string
	SourceResourceID string `json:"sourceResourceId"`
}

type DiskProperties struct {
	//TimeCreated       time.Time //??? 序列化出错？
	OsType            string       `json:"osType"`
	CreationData      CreationData `json:"creationData"`
	DiskSizeGB        int32        `json:"diskSizeGB"`
	ProvisioningState string       `json:"provisioningState,omitempty"`
}

type SDisk struct {
	storage *SStorage

	ManagedBy  string
	Sku        DiskSku `json:"sku"`
	Zones      []string
	ID         string
	Name       string `json:"name"`
	Type       string
	Location   string         `json:"location"`
	Properties DiskProperties `json:"properties"`

	Tags map[string]string
}

func (self *SRegion) CreateDisk(storageType string, name string, sizeGb int32, desc string, imageId string) (*SDisk, error) {
	disk := SDisk{
		Name:     name,
		Location: self.Name,
		Sku: DiskSku{
			Name: storageType,
		},
		Properties: DiskProperties{
			CreationData: CreationData{
				CreateOption: "Empty",
			},
			DiskSizeGB: sizeGb,
		},
		Type: "Microsoft.Compute/disks",
	}
	if len(imageId) > 0 {
		image, err := self.GetImage(imageId)
		if err != nil {
			return nil, err
		}
		blobUrl := image.GetBlobUri()
		if len(blobUrl) == 0 {
			return nil, fmt.Errorf("failed to find blobUri for image %s", image.Name)
		}
		disk.Properties.CreationData = CreationData{
			CreateOption: "Import",
			SourceURI:    blobUrl,
		}
		disk.Properties.OsType = image.GetOsType()
	}
	return &disk, self.client.Create(jsonutils.Marshal(disk), &disk)
}

func (self *SRegion) DeleteDisk(diskId string) error {
	return self.deleteDisk(diskId)
}

func (self *SRegion) deleteDisk(diskId string) error {
	return self.client.Delete(diskId)
}

func (self *SRegion) ResizeDisk(diskId string, sizeGb int32) error {
	return self.resizeDisk(diskId, sizeGb)
}

func (self *SRegion) resizeDisk(diskId string, sizeGb int32) error {
	disk, err := self.GetDisk(diskId)
	if err != nil {
		return err
	}
	disk.Properties.DiskSizeGB = sizeGb
	disk.Properties.ProvisioningState = ""
	_, err = self.client.Update(jsonutils.Marshal(disk))
	return err
}

func (self *SRegion) GetDisk(diskId string) (*SDisk, error) {
	disk := SDisk{}
	return &disk, self.client.Get(diskId, &disk)
}

func (self *SRegion) GetDisks() ([]SDisk, error) {
	result := []SDisk{}
	//self.client.ListClassicDisks()
	disks := []SDisk{}
	err := self.client.ListAll("Microsoft.Compute/disks", &disks)
	if err != nil {
		return nil, err
	}
	for i := 0; i < len(disks); i++ {
		if disks[i].Location == self.Name {
			result = append(result, disks[i])
		}
	}
	return result, nil
}

func (self *SDisk) GetMetadata() *jsonutils.JSONDict {
	data := jsonutils.NewDict()
	data.Add(jsonutils.NewString(models.HYPERVISOR_AZURE), "hypervisor")
	return data
}

func (self *SDisk) GetStatus() string {
	status := self.Properties.ProvisioningState
	switch status {
	case "Updating":
		return models.DISK_ALLOCATING
	case "Succeeded":
		return models.DISK_READY
	default:
		log.Errorf("Unknow azure disk status: %s", status)
		return models.DISK_UNKNOWN
	}
}

func (self *SDisk) GetId() string {
	return self.ID
}

func (self *SDisk) Refresh() error {
	if disk, err := self.storage.zone.region.GetDisk(self.ID); err != nil {
		return cloudprovider.ErrNotFound
	} else {
		return jsonutils.Update(self, disk)
	}
}

func (self *SDisk) Delete() error {
	return self.storage.zone.region.deleteDisk(self.ID)
}

func (self *SDisk) Resize(size int64) error {
	return self.storage.zone.region.resizeDisk(self.ID, int32(size))
}

func (self *SDisk) GetName() string {
	if len(self.Name) > 0 {
		return self.Name
	}
	return self.ID
}

func (self *SDisk) GetGlobalId() string {
	return strings.ToLower(self.ID)
}

func (self *SDisk) IsEmulated() bool {
	return false
}

func (self *SDisk) GetIStorge() cloudprovider.ICloudStorage {
	return self.storage
}

func (self *SDisk) GetFsFormat() string {
	return ""
}

func (self *SDisk) GetIsNonPersistent() bool {
	return false
}

func (self *SDisk) GetDriver() string {
	return "scsi"
}

func (self *SDisk) GetCacheMode() string {
	return "none"
}

func (self *SDisk) GetMountpoint() string {
	return ""
}

func (self *SDisk) GetDiskFormat() string {
	return "vhd"
}

func (self *SDisk) GetDiskSizeMB() int {
	return int(self.Properties.DiskSizeGB) * 1024
}

func (self *SDisk) GetIsAutoDelete() bool {
	return false
}

func (self *SDisk) GetTemplateId() string {
	return self.Properties.CreationData.ImageReference.ID
}

func (self *SDisk) GetDiskType() string {
	if len(self.Properties.OsType) > 0 {
		return models.DISK_TYPE_SYS
	}
	return models.DISK_TYPE_DATA
}

func (self *SDisk) CreateISnapshot(name, desc string) (cloudprovider.ICloudSnapshot, error) {
	if snapshot, err := self.storage.zone.region.CreateSnapshot(self.ID, name, desc); err != nil {
		log.Errorf("createSnapshot fail %s", err)
		return nil, err
	} else {
		return snapshot, nil
	}
}

func (self *SDisk) GetISnapshot(snapshotId string) (cloudprovider.ICloudSnapshot, error) {
	return self.GetSnapshotDetail(snapshotId)
}

func (self *SDisk) GetISnapshots() ([]cloudprovider.ICloudSnapshot, error) {
	if snapshots, err := self.storage.zone.region.GetSnapShots(self.ID); err != nil {
		return nil, err
	} else {
		isnapshots := make([]cloudprovider.ICloudSnapshot, len(snapshots))
		for i := 0; i < len(snapshots); i++ {
			isnapshots[i] = &snapshots[i]
		}
		return isnapshots, nil
	}
}

func (self *SDisk) GetBillingType() string {
	return models.BILLING_TYPE_POSTPAID
}

func (self *SDisk) GetExpiredAt() time.Time {
	return time.Now()
}

func (self *SDisk) GetSnapshotDetail(snapshotId string) (*SSnapshot, error) {
	if snapshot, err := self.storage.zone.region.GetSnapshotDetail(snapshotId); err != nil {
		return nil, err
	} else if snapshot.Properties.CreationData.SourceResourceID != self.ID {
		return nil, cloudprovider.ErrNotFound
	} else {
		return snapshot, nil
	}
}

func (region *SRegion) GetSnapshotDetail(snapshotId string) (*SSnapshot, error) {
	snapshot := SSnapshot{region: region}
	return &snapshot, region.client.Get(snapshotId, &snapshot)
}

func (region *SRegion) GetSnapShots(diskId string) ([]SSnapshot, error) {
	result := []SSnapshot{}
	snapshots := []SSnapshot{}
	err := region.client.ListAll("Microsoft.Compute/snapshots", &snapshots)
	if err != nil {
		return nil, err
	}
	for i := 0; i < len(snapshots); i++ {
		if snapshots[i].Location == region.Name {
			if len(diskId) == 0 || diskId == snapshots[i].Properties.CreationData.SourceResourceID {
				snapshots[i].region = region
				result = append(result, snapshots[i])
			}
		}
	}
	return result, nil
}

func (self *SDisk) Reset(snapshotId string) error {
	return self.storage.zone.region.resetDisk(self.ID, snapshotId)
}

func (self *SRegion) resetDisk(diskId, snapshotId string) error {
	return cloudprovider.ErrNotSupported
}
