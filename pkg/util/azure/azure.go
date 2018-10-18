package azure

import (
	"context"
	"fmt"
	"io/ioutil"
	"net/http"
	"strings"
	"time"

	"github.com/Azure/azure-sdk-for-go/services/consumption/mgmt/2018-03-31/consumption"

	"github.com/Azure/go-autorest/autorest"
	azureenv "github.com/Azure/go-autorest/autorest/azure"
	"github.com/Azure/go-autorest/autorest/azure/auth"

	"yunion.io/x/jsonutils"
	"yunion.io/x/log"
	"yunion.io/x/onecloud/pkg/cloudprovider"
	"yunion.io/x/onecloud/pkg/compute/models"
	"yunion.io/x/onecloud/pkg/httperrors"
)

const (
	CLOUD_PROVIDER_AZURE    = models.CLOUD_PROVIDER_AZURE
	CLOUD_PROVIDER_AZURE_CN = "微软"

	AZURE_API_VERSION = "2016-02-01"
)

type SAzureClient struct {
	client           autorest.Client
	providerId       string
	providerName     string
	subscriptionId   string
	tenantId         string
	clientId         string
	clientScret      string
	domain           string
	baseUrl          string
	secret           string
	envName          string
	subscriptionName string
	env              azureenv.Environment
	authorizer       autorest.Authorizer
	iregions         []cloudprovider.ICloudRegion
}

var DEFAULT_API_VERSION = map[string]string{
	"Microsoft.Compute/virtualMachines":              "2018-04-01",
	"Microsoft.ClassicCompute/virtualMachines":       "2017-04-01",
	"Microsoft.Compute/operations":                   "2018-10-01",
	"Microsoft.ClassicCompute/operations":            "2017-04-01",
	"Microsoft.Network/virtualNetworks":              "2018-08-01",
	"Microsoft.ClassicNetwork/virtualNetworks":       "2017-11-15", //avaliable 2014-01-01,2014-06-01,2015-06-01,2015-12-01,2016-04-01,2016-11-01,2017-11-15
	"Microsoft.Compute/disks":                        "2018-06-01", //avaliable 2016-04-30-preview,2017-03-30,2018-04-01,2018-06-01
	"Microsoft.Storage/storageAccounts":              "2016-12-01", //2018-03-01-preview,2018-02-01,2017-10-01,2017-06-01,2016-12-01,2016-05-01,2016-01-01,2015-06-15,2015-05-01-preview
	"Microsoft.ClassicStorage/storageAccounts":       "2016-04-01", //2014-01-01,2014-04-01,2014-04-01-beta,2014-06-01,2015-06-01,2015-12-01,2016-04-01,2016-11-01
	"Microsoft.Compute/snapshots":                    "2018-06-01", //2016-04-30-preview,2017-03-30,2018-04-01,2018-06-01
	"Microsoft.Compute/images":                       "2018-10-01", //2016-04-30-preview,2016-08-30,2017-03-30,2017-12-01,2018-04-01,2018-06-01,2018-10-01
	"Microsoft.Storage":                              "2016-12-01", //2018-03-01-preview,2018-02-01,2017-10-01,2017-06-01,2016-12-01,2016-05-01,2016-01-01,2015-06-15,2015-05-01-preview
	"Microsoft.Network/publicIPAddresses":            "2018-06-01", //2014-12-01-preview, 2015-05-01-preview, 2015-06-15, 2016-03-30, 2016-06-01, 2016-07-01, 2016-08-01, 2016-09-01, 2016-10-01, 2016-11-01, 2016-12-01, 2017-03-01, 2017-04-01, 2017-06-01, 2017-08-01, 2017-09-01, 2017-10-01, 2017-11-01, 2018-01-01, 2018-02-01, 2018-03-01, 2018-04-01, 2018-05-01, 2018-06-01, 2018-07-01, 2018-08-01
	"Microsoft.Network/networkSecurityGroups":        "2018-06-01",
	"Microsoft.Network/networkInterfaces":            "2018-06-01", //2014-12-01-preview, 2015-05-01-preview, 2015-06-15, 2016-03-30, 2016-06-01, 2016-07-01, 2016-08-01, 2016-09-01, 2016-10-01, 2016-11-01, 2016-12-01, 2017-03-01, 2017-04-01, 2017-06-01, 2017-08-01, 2017-09-01, 2017-10-01, 2017-11-01, 2018-01-01, 2018-02-01, 2018-03-01, 2018-04-01, 2018-05-01, 2018-06-01, 2018-07-01, 2018-08-01
	"Microsoft.Network":                              "2018-06-01",
	"Microsoft.ClassicNetwork/reservedIps":           "2016-04-01", //2014-01-01,2014-06-01,2015-06-01,2015-12-01,2016-04-01,2016-11-01
	"Microsoft.ClassicNetwork/networkSecurityGroups": "2016-11-01", //2015-06-01,2015-12-01,2016-04-01,2016-11-01
}

func NewAzureClient(providerId string, providerName string, accessKey string, secret string, envName string) (*SAzureClient, error) {
	if clientInfo, accountInfo := strings.Split(secret, "/"), strings.Split(accessKey, "/"); len(clientInfo) >= 2 && len(accountInfo) >= 1 {
		client := SAzureClient{providerId: providerId, providerName: providerName, secret: secret, envName: envName}
		client.clientId, client.clientScret = clientInfo[0], strings.Join(clientInfo[1:], "/")
		client.tenantId = accountInfo[0]
		if len(accountInfo) == 2 {
			client.subscriptionId = accountInfo[1]
		}
		err := client.fetchRegions()
		if err != nil {
			return nil, err
		}
		return &client, nil
	} else {
		return nil, httperrors.NewUnauthorizedError("clientId、clientScret or subscriptId input error")
	}
}

func (self *SAzureClient) getDefaultClient() (*autorest.Client, error) {
	client := autorest.NewClientWithUserAgent("hello")
	conf := auth.NewClientCredentialsConfig(self.clientId, self.clientScret, self.tenantId)
	env, err := azureenv.EnvironmentFromName(self.envName)
	if err != nil {
		return nil, err
	}
	self.env = env
	self.domain = env.ResourceManagerEndpoint
	conf.Resource = env.ResourceManagerEndpoint
	conf.AADEndpoint = env.ActiveDirectoryEndpoint
	authorizer, err := conf.Authorizer()
	if err != nil {
		return nil, err
	}
	client.Authorizer = authorizer
	// client.RequestInspector = LogRequest()
	// client.ResponseInspector = LogResponse()
	return &client, nil
}

func (self *SAzureClient) jsonRequest(method, url string, body string) (jsonutils.JSONObject, error) {
	cli, err := self.getDefaultClient()
	if err != nil {
		return nil, err
	}
	version := AZURE_API_VERSION
	for resourceType, _version := range DEFAULT_API_VERSION {
		if strings.Index(strings.ToLower(url), strings.ToLower(resourceType)) > 0 {
			version = _version
		}
	}
	return jsonRequest(cli, method, version, self.domain, url, body)
}

func (self *SAzureClient) Get(resourceId string, retVal interface{}) error {
	cli, err := self.getDefaultClient()
	if err != nil {
		return err
	}
	version := AZURE_API_VERSION
	for resourceType, _version := range DEFAULT_API_VERSION {
		if strings.Index(strings.ToLower(resourceId), strings.ToLower(resourceType)) > 0 {
			version = _version
		}
	}
	body, err := jsonRequest(cli, "GET", version, self.domain, resourceId, "")
	if err != nil {
		return err
	}
	err = body.Unmarshal(retVal)
	if err != nil {
		return err
	}
	return nil
}

func (self *SAzureClient) ListVmSizes(location string) (jsonutils.JSONObject, error) {
	cli, err := self.getDefaultClient()
	if err != nil {
		return nil, err
	}
	if len(self.subscriptionId) == 0 {
		return nil, fmt.Errorf("need subscription id")
	}
	url := fmt.Sprintf("/subscriptions/%s/providers/Microsoft.Compute/locations/%s/vmSizes", self.subscriptionId, location)
	return jsonRequest(cli, "GET", "2018-06-01", self.domain, url, "")
}

func (self *SAzureClient) ListClassicDisks() (jsonutils.JSONObject, error) {
	cli, err := self.getDefaultClient()
	if err != nil {
		return nil, err
	}
	if len(self.subscriptionId) == 0 {
		return nil, fmt.Errorf("need subscription id")
	}
	url := fmt.Sprintf("/subscriptions/%s/services/disks", self.subscriptionId)
	return jsonRequest(cli, "GET", "2018-06-01", self.domain, url, "")
}

func (self *SAzureClient) ListAll(resourceType string, retVal interface{}) error {
	cli, err := self.getDefaultClient()
	if err != nil {
		return err
	}
	url := "/subscriptions"
	if len(self.subscriptionId) > 0 {
		url += fmt.Sprintf("/%s", self.subscriptionId)
	}
	version := AZURE_API_VERSION
	if len(resourceType) > 0 {
		url += fmt.Sprintf("/providers/%s", resourceType)
		if _version, ok := DEFAULT_API_VERSION[resourceType]; ok {
			version = _version
		}
	}
	body, err := jsonRequest(cli, "GET", version, self.domain, url, "")
	if err != nil {
		return err
	}
	return body.Unmarshal(retVal, "value")
}

func (self *SAzureClient) ListSubscriptions() (jsonutils.JSONObject, error) {
	cli, err := self.getDefaultClient()
	if err != nil {
		return nil, err
	}
	return jsonRequest(cli, "GET", AZURE_API_VERSION, self.domain, "/subscriptions", "")
}

func (self *SAzureClient) List(golbalResource string, retVal interface{}) error {
	cli, err := self.getDefaultClient()
	if err != nil {
		return err
	}
	url := "/subscriptions"
	if len(self.subscriptionId) > 0 {
		url += fmt.Sprintf("/%s", self.subscriptionId)
	}
	if len(self.subscriptionId) > 0 && len(golbalResource) > 0 {
		url += fmt.Sprintf("/%s", golbalResource)
	}
	body, err := jsonRequest(cli, "GET", AZURE_API_VERSION, self.domain, url, "")
	if err != nil {
		return err
	}
	return body.Unmarshal(retVal, "value")
}

func (self *SAzureClient) ListByType(Type string, retVal interface{}) error {
	cli, err := self.getDefaultClient()
	if err != nil {
		return err
	}
	if len(self.subscriptionId) == 0 {
		return fmt.Errorf("Missing subscription Info")
	}
	resourceGroupName, ok := defaultResourceGroups[Type]
	if !ok {
		return fmt.Errorf("Not find default resourceGroup for %s", Type)
	}
	version := AZURE_API_VERSION
	if _version, ok := DEFAULT_API_VERSION[Type]; ok {
		version = _version
	}

	url := fmt.Sprintf("/subscriptions/%s/resourceGroups/%s/providers/%s", self.subscriptionId, resourceGroupName, Type)
	body, err := jsonRequest(cli, "GET", version, self.domain, url, "")
	if err != nil {
		return err
	}
	return body.Unmarshal(retVal, "value")
}

func (self *SAzureClient) Delete(resourceId string) error {
	cli, err := self.getDefaultClient()
	if err != nil {
		return err
	}
	version := AZURE_API_VERSION
	for resourceType, _version := range DEFAULT_API_VERSION {
		if strings.Index(resourceId, resourceType) > 0 {
			version = _version
		}
	}
	_, err = jsonRequest(cli, "DELETE", version, self.domain, resourceId, "")
	return err
}

func (self *SAzureClient) PerformAction(resourceId string, action string) (jsonutils.JSONObject, error) {
	cli, err := self.getDefaultClient()
	if err != nil {
		return nil, err
	}
	version := AZURE_API_VERSION
	for resourceType, _version := range DEFAULT_API_VERSION {
		if strings.Index(resourceId, resourceType) > 0 {
			version = _version
		}
	}
	url := fmt.Sprintf("%s/%s", resourceId, action)
	return jsonRequest(cli, "POST", version, self.domain, url, "")
}

func (self *SAzureClient) Create(body jsonutils.JSONObject, retVal interface{}) error {
	cli, err := self.getDefaultClient()
	if err != nil {
		return err
	}
	url := "/subscriptions"
	if len(self.subscriptionId) == 0 {
		return fmt.Errorf("Missing subscription info")
	}
	url += fmt.Sprintf("/%s", self.subscriptionId)
	Type, err := body.GetString("type")
	if err != nil {
		return err
	}
	if resourceGroupName, ok := defaultResourceGroups[Type]; ok {
		url += fmt.Sprintf("/resourceGroups/%s/providers/%s", resourceGroupName, Type)
	} else {
		msg := fmt.Sprintf("Create %s Missing resourceGroupName", Type)
		return fmt.Errorf(msg)
	}

	version := AZURE_API_VERSION
	if _version, ok := DEFAULT_API_VERSION[Type]; ok {
		version = _version
	}
	name, err := body.GetString("name")
	if err != nil {
		log.Errorf("Create %s error: Missing name params", Type)
		return err
	}
	url += fmt.Sprintf("/%s", name)

	result, err := jsonRequest(cli, "PUT", version, self.domain, url, body.String())
	if err != nil {
		return err
	}
	return result.Unmarshal(retVal)
}

func (self *SAzureClient) CheckNameAvailability(Type string, body string) (jsonutils.JSONObject, error) {
	cli, err := self.getDefaultClient()
	if err != nil {
		return nil, err
	}
	if len(self.subscriptionId) == 0 {
		return nil, fmt.Errorf("Missing subscription ID")
	}
	url := fmt.Sprintf("/subscriptions/%s/providers/%s/checkNameAvailability", self.subscriptionId, Type)
	version := AZURE_API_VERSION
	for resourceType, _version := range DEFAULT_API_VERSION {
		if strings.Index(url, resourceType) > 0 {
			version = _version
		}
	}
	return jsonRequest(cli, "POST", version, self.domain, url, body)
}

func (self *SAzureClient) Update(body jsonutils.JSONObject) (jsonutils.JSONObject, error) {
	cli, err := self.getDefaultClient()
	if err != nil {
		return nil, err
	}
	url, err := body.GetString("id")
	version := AZURE_API_VERSION
	for resourceType, _version := range DEFAULT_API_VERSION {
		if strings.Index(url, resourceType) > 0 {
			version = _version
		}
	}
	return jsonRequest(cli, "PUT", version, self.domain, url, body.String())
}

func jsonRequest(client *autorest.Client, method, version, domain, baseUrl string, body string) (jsonutils.JSONObject, error) {
	return _jsonRequest(client, method, version, domain, baseUrl, body)
}

func _jsonRequest(client *autorest.Client, method, version, domain, baseUrl string, body string) (result jsonutils.JSONObject, err error) {
	url := fmt.Sprintf("%s%s?api-version=%s", domain, baseUrl, version)
	if strings.Index(baseUrl, "?") > 0 {
		url = fmt.Sprintf("%s%s&api-version=%s", domain, baseUrl, version)
	}
	req := &http.Request{}
	if len(body) != 0 {
		req, err = http.NewRequest(method, url, strings.NewReader(body))
		if err != nil {
			return nil, err
		}
	} else {
		req, err = http.NewRequest(method, url, nil)
		if err != nil {
			return nil, err
		}
	}
	req.Header.Add("Content-Type", "application/json; charset=utf-8")
	resp, err := client.Do(req)
	if err != nil {
		return nil, err
	}

	if resp.StatusCode == 404 {
		data := []byte{}
		if resp.ContentLength != 0 {
			data, _ = ioutil.ReadAll(resp.Body)
		}
		log.Errorf("failed find %s error: %s", url, string(data))
		return nil, cloudprovider.ErrNotFound
	}

	location := resp.Header.Get("Location")
	asyncoperation := resp.Header.Get("Azure-Asyncoperation")
	if len(location) > 0 || (len(asyncoperation) > 0 && resp.StatusCode != 200) {
		if len(asyncoperation) > 0 {
			location = asyncoperation
		}
		for {
			asyncReq, err := http.NewRequest("GET", location, nil)
			if err != nil {
				return nil, err
			}
			asyncResp, err := client.Do(asyncReq)
			if err != nil {
				return nil, err
			}
			if asyncResp.StatusCode == 202 {
				time.Sleep(time.Second * 5)
				continue
			}
			if asyncResp.ContentLength == 0 {
				return jsonutils.NewDict(), nil
			}
			data, err := ioutil.ReadAll(asyncResp.Body)
			if err != nil {
				return nil, err
			}
			asyncData, err := jsonutils.Parse(data)
			if err != nil {
				return nil, err
			}
			if len(asyncoperation) > 0 && asyncData.Contains("status") {
				status, _ := asyncData.GetString("status")
				if status == "InProgress" {
					continue
				}
				if status == "Succeeded" {
					break
				}
				return nil, fmt.Errorf("Create %s failed: %s", body, data)
			}
			return asyncData, nil
		}
	}

	if resp.ContentLength == 0 {
		return jsonutils.NewDict(), nil
	}
	data, err := ioutil.ReadAll(resp.Body)
	if err != nil {
		return nil, err
	}
	_data := strings.Replace(string(data), "\r", "", -1)
	result, err = jsonutils.Parse([]byte(_data))
	if err != nil {
		return nil, err
	}
	if result.Contains("error") {
		return nil, fmt.Errorf(result.String())
	}
	return result, nil
}

func (self *SAzureClient) UpdateAccount(tenantId, secret, envName string) error {
	if self.tenantId != tenantId || self.secret != secret || self.envName != envName {
		if clientInfo, accountInfo := strings.Split(secret, "/"), strings.Split(tenantId, "/"); len(clientInfo) >= 2 && len(accountInfo) >= 1 {
			self.clientId, self.clientScret = clientInfo[0], strings.Join(clientInfo[1:], "/")
			self.tenantId = accountInfo[0]
			if len(accountInfo) == 2 {
				self.subscriptionId = accountInfo[1]
			}
			err := self.fetchRegions()
			if err != nil {
				return err
			}
			return nil
		} else {
			return httperrors.NewUnauthorizedError("clientId、clientScret or subscriptId input error")
		}
	}
	return nil
}

func (self *SAzureClient) fetchRegions() error {
	if len(self.subscriptionId) > 0 {
		regions := []SRegion{}
		err := self.List("locations", &regions)
		if err != nil {
			return err
		}
		self.iregions = make([]cloudprovider.ICloudRegion, len(regions))
		for i := 0; i < len(regions); i++ {
			regions[i].client = self
			regions[i].SubscriptionID = self.subscriptionId
			self.iregions[i] = &regions[i]
		}
	}
	body, err := self.ListSubscriptions()
	if err != nil {
		return err
	}
	subscriptions, err := body.GetArray("value")
	if err != nil {
		return err
	}
	for _, subscription := range subscriptions {
		subscriptionId, _ := subscription.GetString("subscriptionId")
		if subscriptionId == self.subscriptionId {
			self.subscriptionName, _ = subscription.GetString("displayName")
			break
		}
	}
	return nil
}

func (self *SAzureClient) GetRegions() []SRegion {
	regions := make([]SRegion, len(self.iregions))
	for i := 0; i < len(regions); i += 1 {
		region := self.iregions[i].(*SRegion)
		regions[i] = *region
	}
	return regions
}

func (self *SAzureClient) GetSubAccounts() (jsonutils.JSONObject, error) {
	body, err := self.ListSubscriptions()
	if err != nil {
		return nil, err
	}
	value, err := body.GetArray("value")
	if err != nil {
		return nil, err
	}
	result := jsonutils.NewDict()
	result.Add(jsonutils.NewInt(int64(len(value))), "total")
	result.Add(jsonutils.NewArray(value...), "data")
	return result, nil
}

func (self *SAzureClient) GetIRegions() []cloudprovider.ICloudRegion {
	return self.iregions
}

func (self *SAzureClient) getDefaultRegion() (cloudprovider.ICloudRegion, error) {
	if len(self.iregions) > 0 {
		return self.iregions[0], nil
	}
	return nil, cloudprovider.ErrNotFound
}

func (self *SAzureClient) GetIRegionById(id string) (cloudprovider.ICloudRegion, error) {
	for i := 0; i < len(self.iregions); i += 1 {
		if self.iregions[i].GetGlobalId() == id {
			return self.iregions[i], nil
		}
	}
	return nil, cloudprovider.ErrNotFound
}

func (self *SAzureClient) GetRegion(regionId string) *SRegion {
	for i := 0; i < len(self.iregions); i += 1 {
		if self.iregions[i].GetId() == regionId {
			return self.iregions[i].(*SRegion)
		}
	}
	return nil
}

func (self *SAzureClient) GetIHostById(id string) (cloudprovider.ICloudHost, error) {
	for i := 0; i < len(self.iregions); i += 1 {
		ihost, err := self.iregions[i].GetIHostById(id)
		if err == nil {
			return ihost, nil
		} else if err != cloudprovider.ErrNotFound {
			return nil, err
		}
	}
	return nil, cloudprovider.ErrNotFound
}

func (self *SAzureClient) GetIVpcById(id string) (cloudprovider.ICloudVpc, error) {
	for i := 0; i < len(self.iregions); i += 1 {
		ihost, err := self.iregions[i].GetIVpcById(id)
		if err == nil {
			return ihost, nil
		} else if err != cloudprovider.ErrNotFound {
			return nil, err
		}
	}
	return nil, cloudprovider.ErrNotFound
}

func (self *SAzureClient) GetIStorageById(id string) (cloudprovider.ICloudStorage, error) {
	for i := 0; i < len(self.iregions); i += 1 {
		ihost, err := self.iregions[i].GetIStorageById(id)
		if err == nil {
			return ihost, nil
		} else if err != cloudprovider.ErrNotFound {
			return nil, err
		}
	}
	return nil, cloudprovider.ErrNotFound
}

func (self *SAzureClient) GetIStoragecacheById(id string) (cloudprovider.ICloudStoragecache, error) {
	for i := 0; i < len(self.iregions); i += 1 {
		ihost, err := self.iregions[i].GetIStoragecacheById(id)
		if err == nil {
			return ihost, nil
		} else if err != cloudprovider.ErrNotFound {
			return nil, err
		}
	}
	return nil, cloudprovider.ErrNotFound
}

type SAccountBalance struct {
	AvailableAmount     float64
	AvailableCashAmount float64
	CreditAmount        float64
	MybankCreditAmount  float64
	Currency            string
}

func (self *SAzureClient) QueryAccountBalance() (*SAccountBalance, error) {
	consumption.NewWithBaseURI(self.baseUrl, self.subscriptionId)
	costClient := consumption.NewWithBaseURI(self.baseUrl, self.subscriptionId)
	//costClient := costmanagement.NewBillingAccountDimensionsClientWithBaseURI(self.baseUrl, self.subscriptionId)
	costClient.Authorizer = self.authorizer
	if result, err := costClient.GetBalancesByBillingAccount(context.Background(), "quxuan@ioito.partner.onmschina.cn"); err != nil {
		//if result, err := costClient.Get(context.Background(), ""); err != nil {
		return nil, err
	} else {
		log.Errorf(jsonutils.Marshal(result).PrettyString())
	}
	return nil, nil
}
