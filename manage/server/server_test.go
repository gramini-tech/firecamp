package manageserver

import (
	"bytes"
	"encoding/json"
	"flag"
	"io/ioutil"
	"net/http"
	"net/http/httptest"
	"net/url"
	"strconv"
	"testing"
	"time"

	"golang.org/x/net/context"

	"github.com/aws/aws-sdk-go/aws"
	"github.com/aws/aws-sdk-go/aws/session"
	"github.com/golang/glog"

	"github.com/cloudstax/openmanage/common"
	"github.com/cloudstax/openmanage/containersvc"
	"github.com/cloudstax/openmanage/db"
	"github.com/cloudstax/openmanage/db/awsdynamodb"
	"github.com/cloudstax/openmanage/db/controldb/client"
	"github.com/cloudstax/openmanage/dns"
	"github.com/cloudstax/openmanage/manage"
	"github.com/cloudstax/openmanage/server"
	"github.com/cloudstax/openmanage/utils"
)

var region = flag.String("region", "us-west-1", "The target AWS region for DynamoDB")

func TestServerMgrOperationsWithMemDB(t *testing.T) {
	flag.Parse()
	//flag.Set("stderrthreshold", "FATAL")

	cluster := "cluster1"
	manageurl := dns.GetDefaultManageServiceURL(cluster, false)
	dbIns := db.NewMemDB()
	dnsIns := dns.NewMockDNS()
	serverIns := server.NewMemServer()
	serverInfo := server.NewMockServerInfo()
	containersvcIns := containersvc.NewMemContainerSvc()

	ctx := context.Background()

	mgtsvc := NewManageHTTPServer(cluster, manageurl, dbIns, dnsIns, serverIns, serverInfo, containersvcIns)
	serviceNum := 29
	testMgrOps(ctx, t, mgtsvc, serviceNum)
}

func TestServerMgrOperationsWithControlDB(t *testing.T) {
	flag.Parse()
	//flag.Set("stderrthreshold", "INFO")

	testdir := "/tmp/test-" + strconv.FormatInt((time.Now().UnixNano()), 10)
	cluster := "cluster1"
	manageurl := dns.GetDefaultManageServiceURL(cluster, false)

	s := &controldbcli.TestControlDBServer{Testdir: testdir, ListenPort: common.ControlDBServerPort + 2}
	go s.RunControldbTestServer(cluster)
	defer s.StopControldbTestServer()

	dbcli := controldbcli.NewControlDBCli("localhost:" + strconv.Itoa(common.ControlDBServerPort+2))
	dnsIns := dns.NewMockDNS()
	serverIns := server.NewMemServer()
	serverInfo := server.NewMockServerInfo()
	containersvcIns := containersvc.NewMemContainerSvc()

	ctx := context.Background()

	mgtsvc := NewManageHTTPServer(cluster, manageurl, dbcli, dnsIns, serverIns, serverInfo, containersvcIns)
	serviceNum := 15
	testMgrOps(ctx, t, mgtsvc, serviceNum)
}

func TestServerMgrOperationsWithDynamoDB(t *testing.T) {
	flag.Parse()
	//flag.Set("stderrthreshold", "INFO")

	config := aws.NewConfig().WithRegion(*region)
	sess, err := session.NewSession(config)
	if err != nil {
		t.Fatalf("create aws session error", err, *region)
	}

	ctx := context.Background()

	tableNameSuffix := utils.GenUUID()
	dbIns := awsdynamodb.NewTestDynamoDB(sess, tableNameSuffix)
	err = dbIns.CreateSystemTables(ctx)
	defer dbIns.DeleteSystemTables(ctx)
	if err != nil {
		t.Fatalf("create system table error", err, "region", *region, "tableNameSuffix", tableNameSuffix)
	}

	err = dbIns.WaitSystemTablesReady(ctx, 120)
	if err != nil {
		t.Fatalf("WaitSystemTablesReady error", err)
	}

	dnsIns := dns.NewMockDNS()
	serverIns := server.NewMemServer()
	serverInfo := server.NewMockServerInfo()
	containersvcIns := containersvc.NewMemContainerSvc()

	cluster := "cluster1"
	manageurl := dns.GetDefaultManageServiceURL(cluster, false)
	mgtsvc := NewManageHTTPServer(cluster, manageurl, dbIns, dnsIns, serverIns, serverInfo, containersvcIns)
	serviceNum := 7
	testMgrOps(ctx, t, mgtsvc, serviceNum)
}

func testMgrOps(ctx context.Context, t *testing.T, mgtsvc *ManageHTTPServer, serviceNum int) {
	// create services
	servicePrefix := "service-"
	requuidPrefix := "requuid-"
	for taskCount := 1; taskCount < serviceNum+1; taskCount++ {
		service := servicePrefix + strconv.Itoa(taskCount)
		requuid := requuidPrefix + strconv.Itoa(taskCount)

		r := genCreateRequest(service, taskCount, mgtsvc, t)
		w := httptest.NewRecorder()
		unescapedURL, _ := url.QueryUnescape(r.URL.String())

		errmsg, errcode := mgtsvc.putOp(ctx, w, r, unescapedURL, requuid)
		if errcode != http.StatusOK {
			t.Fatalf("create service expect http.StatusOK, got %d, %s", errcode, errmsg)
		}

		listVolumesTest(ctx, t, mgtsvc, taskCount, service)
	}

	// list services with and without prefix
	listServicesTest(ctx, t, mgtsvc, serviceNum, "")
	listServicesTest(ctx, t, mgtsvc, serviceNum, servicePrefix)
	// negative case: list non-exist prefix
	listServicesTest(ctx, t, mgtsvc, 0, "xxxx")

	// get service
	for i := 1; i < serviceNum+1; i++ {
		getServiceAttrTest(ctx, t, mgtsvc, servicePrefix, requuidPrefix, i, common.ServiceStatusInitializing)
	}

	// negative case: get non-exist service
	r := genGetServiceAttrRequest("xxxx", mgtsvc, t)
	w := httptest.NewRecorder()
	unescapedURL, _ := url.QueryUnescape(r.URL.String())

	errmsg, errcode := mgtsvc.getOp(ctx, w, r, unescapedURL, requuidPrefix+"get")
	if errcode != http.StatusNotFound {
		t.Fatalf("get non-exist service, expect StatusNotFound, got %d, %s", w.Code, w)
	}

	// set service initialized
	for i := 1; i < serviceNum+1; i++ {
		service := servicePrefix + strconv.Itoa(i)
		requuid := requuidPrefix + strconv.Itoa(i)

		r := genSetInitRequest(service, mgtsvc, t)
		w := httptest.NewRecorder()
		unescapedURL, _ := url.QueryUnescape(r.URL.String())

		errmsg, errcode := mgtsvc.putOp(ctx, w, r, unescapedURL, requuid)

		if errcode != http.StatusOK {
			t.Fatalf("create service expect http.StatusOK, got %d, %s", errcode, errmsg)
		}
	}
	// get service attr again to check status is active
	for i := 1; i < serviceNum+1; i++ {
		getServiceAttrTest(ctx, t, mgtsvc, servicePrefix, requuidPrefix, i, common.ServiceStatusActive)
	}

	// delete 1/5 service
	delNum := 0
	for i := 1; i < serviceNum+1; i += 5 {
		service := servicePrefix + strconv.Itoa(i)
		requuid := requuidPrefix + strconv.Itoa(i)

		r := genDeleteRequest(service, mgtsvc, t)
		w := httptest.NewRecorder()
		unescapedURL, _ = url.QueryUnescape(r.URL.String())

		errmsg, errcode := mgtsvc.delOp(ctx, w, r, unescapedURL, requuid)
		if errcode != http.StatusOK {
			t.Fatalf("get non-exist service, expect StatusOK, got %d, %s", errcode, errmsg)
		}
		delNum++
	}

	// list services again
	listServicesTest(ctx, t, mgtsvc, serviceNum-delNum, "")

	// negative case: delete non-exist service
	r = genDeleteRequest("xxxx", mgtsvc, t)
	w = httptest.NewRecorder()
	unescapedURL, _ = url.QueryUnescape(r.URL.String())

	errmsg, errcode = mgtsvc.delOp(ctx, w, r, unescapedURL, requuidPrefix+"del")
	if errcode != http.StatusNotFound {
		t.Fatalf("get non-exist service, expect StatusNotFound, got %d, %s", errcode, errmsg)
	}
}

func listServicesTest(ctx context.Context, t *testing.T, mgtsvc *ManageHTTPServer, serviceNum int, prefix string) {
	requuid := "requuid-" + "list"
	r := genListServiceRequest(prefix, mgtsvc, t)
	w := httptest.NewRecorder()
	unescapedURL, _ := url.QueryUnescape(r.URL.String())

	errmsg, errcode := mgtsvc.getOp(ctx, w, r, unescapedURL, requuid)
	if errcode != http.StatusOK {
		t.Fatalf("list services expect StatusOK, got %d, %s", errcode, errmsg)
	}
	if w.Body.Len() == 0 {
		t.Fatalf("list services, got 0 len body, %s", w)
	}

	res := &manage.ListServiceResponse{}
	err := json.Unmarshal(w.Body.Bytes(), res)
	if err != nil {
		t.Fatalf("Unmarshal ListServiceResponse error %s, %s", err, w)
	}
	if len(res.Services) != serviceNum {
		t.Fatalf("ListServiceResponse expect %d services, got %d, %s", serviceNum, len(res.Services), res)
	}
	glog.Infoln("listServicesResult", res)
}

func listVolumesTest(ctx context.Context, t *testing.T, mgtsvc *ManageHTTPServer, volNum int, service string) {
	requuid := "requuid-" + "list"
	r := genListVolumeRequest(service, mgtsvc, t)
	w := httptest.NewRecorder()
	unescapedURL, _ := url.QueryUnescape(r.URL.String())

	errmsg, errcode := mgtsvc.getOp(ctx, w, r, unescapedURL, requuid)
	if errcode != http.StatusOK {
		t.Fatalf("list volumes expect StatusOK, got %d, %s", errcode, errmsg)
	}
	if w.Body.Len() == 0 {
		t.Fatalf("list volumes, got 0 len body, %s", w)
	}

	res := &manage.ListVolumeResponse{}
	err := json.Unmarshal(w.Body.Bytes(), res)
	if err != nil {
		t.Fatalf("Unmarshal ListVolumeResponse error %s, %s", err, w)
	}
	if len(res.Volumes) != volNum {
		t.Fatalf("ListVolumeResponse expect %d volumes, got %d, %s", volNum, len(res.Volumes), res)
	}
	glog.Infoln("ListVolumeResponse", res)
}

func getServiceAttrTest(ctx context.Context, t *testing.T, mgtsvc *ManageHTTPServer, servicePrefix string, requuidPrefix string, i int, targetServiceStatus string) {
	service := servicePrefix + strconv.Itoa(i)
	requuid := requuidPrefix + strconv.Itoa(i)
	r := genGetServiceAttrRequest(service, mgtsvc, t)
	w := httptest.NewRecorder()
	unescapedURL, _ := url.QueryUnescape(r.URL.String())

	errmsg, errcode := mgtsvc.getOp(ctx, w, r, unescapedURL, requuid)
	if errcode != http.StatusOK {
		t.Fatalf("get service expect http.StatusOK, got %d, %s", errcode, errmsg)
	}

	res := &manage.GetServiceAttributesResponse{}
	err := json.Unmarshal(w.Body.Bytes(), res)
	if err != nil {
		t.Fatalf("Unmarshal GetServiceAttributesResponse error %s, %s", err, w)
	}
	if res.Service.ServiceName != service || res.Service.ServiceStatus != targetServiceStatus ||
		res.Service.Replicas != int64(i) || res.Service.VolumeSizeGB != int64(i+1) {
		t.Fatalf("expect service %s status %s TaskCounts %d VolumeSize %d, got %s", service, targetServiceStatus, i, i+1, res.Service)
	}
	glog.Infoln("GetServiceAttributesResponse", res)
}

func genCreateRequest(service string, taskCount int, mgtsvc *ManageHTTPServer, t *testing.T) *http.Request {
	replicaCfgs := make([]*manage.ReplicaConfig, taskCount)
	for i := 0; i < taskCount; i++ {
		cfg := &manage.ReplicaConfigFile{FileName: service, Content: service}
		configs := []*manage.ReplicaConfigFile{cfg}
		replicaCfg := &manage.ReplicaConfig{Zone: "west-az-1", Configs: configs}
		replicaCfgs[i] = replicaCfg
	}

	req := &manage.CreateServiceRequest{
		Service: &manage.ServiceCommonRequest{
			Region:      mgtsvc.region,
			Cluster:     mgtsvc.cluster,
			ServiceName: service,
		},
		Resource: &common.Resources{
			MaxCPUUnits:     2,
			ReserveCPUUnits: 2,
			MaxMemMB:        2,
			ReserveMemMB:    2,
		},

		ContainerImage: "image",
		Replicas:       int64(taskCount),
		VolumeSizeGB:   int64(taskCount + 1),
		ContainerPath:  "",
		HasMembership:  true,
		ReplicaConfigs: replicaCfgs,
	}

	b, err := json.Marshal(req)
	if err != nil {
		t.Fatalf("Marshal CreateServiceRequest error %s", err)
	}

	body := ioutil.NopCloser(bytes.NewReader(b))
	return &http.Request{Method: "PUT", URL: &url.URL{Path: service}, Body: body}
}

func genGetServiceAttrRequest(service string, mgtsvc *ManageHTTPServer, t *testing.T) *http.Request {
	req := &manage.ServiceCommonRequest{
		Region:      mgtsvc.region,
		Cluster:     mgtsvc.cluster,
		ServiceName: service,
	}

	b, err := json.Marshal(req)
	if err != nil {
		t.Fatalf("Marshal ServiceCommonRequest error %s", err)
	}

	body := ioutil.NopCloser(bytes.NewReader(b))
	return &http.Request{Method: "GET", URL: &url.URL{Path: service}, Body: body}
}

func genListServiceRequest(prefix string, mgtsvc *ManageHTTPServer, t *testing.T) *http.Request {
	req := &manage.ListServiceRequest{
		Region:  mgtsvc.region,
		Cluster: mgtsvc.cluster,
		Prefix:  prefix,
	}

	b, err := json.Marshal(req)
	if err != nil {
		t.Fatalf("Marshal CreateServiceRequest error %s", err)
	}

	body := ioutil.NopCloser(bytes.NewReader(b))
	return &http.Request{Method: "GET", URL: &url.URL{Path: manage.ListServiceOp}, Body: body}
}

func genListVolumeRequest(service string, mgtsvc *ManageHTTPServer, t *testing.T) *http.Request {
	req := &manage.ListVolumeRequest{
		Service: &manage.ServiceCommonRequest{
			Region:      mgtsvc.region,
			Cluster:     mgtsvc.cluster,
			ServiceName: service,
		},
	}

	b, err := json.Marshal(req)
	if err != nil {
		t.Fatalf("Marshal  error %s", err)
	}

	body := ioutil.NopCloser(bytes.NewReader(b))
	return &http.Request{Method: "GET", URL: &url.URL{Path: manage.ListVolumeOp}, Body: body}
}

func genSetInitRequest(service string, mgtsvc *ManageHTTPServer, t *testing.T) *http.Request {
	req := &manage.ServiceCommonRequest{
		Region:      mgtsvc.region,
		Cluster:     mgtsvc.cluster,
		ServiceName: service,
	}

	b, err := json.Marshal(req)
	if err != nil {
		t.Fatalf("Marshal ServiceCommonRequest error %s", err)
	}

	body := ioutil.NopCloser(bytes.NewReader(b))
	return &http.Request{Method: "PUT", URL: &url.URL{Path: manage.ServiceInitializedOp}, Body: body}
}

func genDeleteRequest(service string, mgtsvc *ManageHTTPServer, t *testing.T) *http.Request {
	req := &manage.ServiceCommonRequest{
		Region:      mgtsvc.region,
		Cluster:     mgtsvc.cluster,
		ServiceName: service,
	}

	b, err := json.Marshal(req)
	if err != nil {
		t.Fatalf("Marshal ServiceCommonRequest error %s", err)
	}

	body := ioutil.NopCloser(bytes.NewReader(b))
	return &http.Request{Method: "DELETE", URL: &url.URL{Path: service}, Body: body}
}
