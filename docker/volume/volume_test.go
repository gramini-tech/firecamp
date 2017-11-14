package firecampdockervolume

import (
	"flag"
	"fmt"
	"os/exec"
	"path/filepath"
	"strconv"
	"testing"
	"time"

	"github.com/docker/go-plugins-helpers/volume"
	"github.com/golang/glog"
	"golang.org/x/net/context"

	"github.com/cloudstax/firecamp/common"
	"github.com/cloudstax/firecamp/containersvc"
	"github.com/cloudstax/firecamp/db"
	"github.com/cloudstax/firecamp/dns"
	"github.com/cloudstax/firecamp/manage"
	"github.com/cloudstax/firecamp/manage/service"
	"github.com/cloudstax/firecamp/server"
	"github.com/cloudstax/firecamp/utils"
)

func TestParseRequestName(t *testing.T) {
	// create FireCampVolumeDriver
	dbIns := db.NewMemDB()
	mockDNS := dns.NewMockDNS()
	serverIns := server.NewLoopServer()
	mockServerInfo := server.NewMockServerInfo()
	contSvcIns := containersvc.NewMemContainerSvc()
	mockContInfo := containersvc.NewMockContainerSvcInfo()

	d := NewVolumeDriver(dbIns, mockDNS, serverIns, mockServerInfo, contSvcIns, mockContInfo)
	d.ifname = "lo"

	serviceuuid := "uuid"
	name := serviceuuid
	uuid, mpath, mindex, err := d.parseRequestName(name)
	if err != nil || uuid != serviceuuid || mpath != serviceuuid || mindex != -1 {
		t.Fatalf("expect uuid %s get %s, mpath %s, mindex %d, err %s", serviceuuid, uuid, mpath, mindex, err)
	}

	name = serviceuuid + "-1"
	uuid, mpath, mindex, err = d.parseRequestName(name)
	if err != nil || uuid != serviceuuid || mpath != serviceuuid || mindex != 0 {
		t.Fatalf("expect uuid %s get %s, mpath %s, mindex %d, err %s", serviceuuid, uuid, mpath, mindex, err)
	}

	name = common.LogDevicePathPrefix + common.NameSeparator + serviceuuid + "-2"
	uuid, mpath, mindex, err = d.parseRequestName(name)
	if err != nil || uuid != serviceuuid || mpath != common.LogDevicePathPrefix+common.NameSeparator+serviceuuid || mindex != 1 {
		t.Fatalf("expect uuid %s get %s, mpath %s, mindex %d, err %s", serviceuuid, uuid, mpath, mindex, err)
	}

	// negative case
	name = serviceuuid + "-aaa-1"
	uuid, mpath, mindex, err = d.parseRequestName(name)
	if err != common.ErrInvalidArgs {
		t.Fatalf("expect err, but get uuid %s, err %s", uuid, err)
	}
	name = serviceuuid + "-aaa-1-1"
	uuid, mpath, mindex, err = d.parseRequestName(name)
	if err != common.ErrInvalidArgs {
		t.Fatalf("expect err, but get uuid %s, err %s", uuid, err)
	}
}

func TestVolumeFunctions(t *testing.T) {
	// create FireCampVolumeDriver
	dbIns := db.NewMemDB()
	mockDNS := dns.NewMockDNS()
	serverIns := server.NewLoopServer()
	mockServerInfo := server.NewMockServerInfo()
	contSvcIns := containersvc.NewMemContainerSvc()
	mockContInfo := containersvc.NewMockContainerSvcInfo()

	mgsvc := manageservice.NewManageService(dbIns, mockServerInfo, serverIns, mockDNS)

	driver := NewVolumeDriver(dbIns, mockDNS, serverIns, mockServerInfo, contSvcIns, mockContInfo)
	driver.ifname = "lo"

	requuid := utils.GenRequestUUID()
	ctx := context.Background()
	ctx = utils.NewRequestContext(ctx, requuid)

	cluster := "cluster1"
	region := "local-region"
	az := "local-az"
	domain := "test.com"
	vpcID := "vpc1"
	taskCounts := 1

	// create the 1st service
	service1 := "service1"

	// create the config files for replicas
	replicaCfgs := make([]*manage.ReplicaConfig, taskCounts)
	for i := 0; i < taskCounts; i++ {
		cfg := &manage.ReplicaConfigFile{FileName: "configfile-name", Content: "configfile-content"}
		configs := []*manage.ReplicaConfigFile{cfg}
		replicaCfg := &manage.ReplicaConfig{Zone: az, Configs: configs}
		replicaCfgs[i] = replicaCfg
	}

	req := &manage.CreateServiceRequest{
		Service: &manage.ServiceCommonRequest{
			Region:      region,
			Cluster:     cluster,
			ServiceName: service1,
		},
		Replicas: int64(taskCounts),
		Volume: &common.ServiceVolume{
			VolumeType:   common.VolumeTypeGPSSD,
			VolumeSizeGB: 1,
		},
		RegisterDNS:    true,
		ReplicaConfigs: replicaCfgs,
	}

	uuid1, err := mgsvc.CreateService(ctx, req, domain, vpcID)
	if err != nil {
		t.Fatalf("CreateService error", err)
	}

	volumeFuncTest(t, driver, uuid1)

	// create the 2nd service
	service2 := "service2"
	req.Service.ServiceName = service2
	req.LogVolume = &common.ServiceVolume{
		VolumeType:   common.VolumeTypeIOPSSSD,
		VolumeSizeGB: 1,
		Iops:         1,
	}

	uuid2, err := mgsvc.CreateService(ctx, req, domain, vpcID)
	if err != nil {
		t.Fatalf("CreateService error", err)
	}

	volumeFuncTest(t, driver, common.LogDevicePathPrefix+common.NameSeparator+uuid2)
}

func TestVolumeDriver(t *testing.T) {
	requireStaticIP := true
	requireLogVolume := true
	testVolumeDriver(t, requireStaticIP, requireLogVolume)
	requireLogVolume = false
	testVolumeDriver(t, requireStaticIP, requireLogVolume)

	requireStaticIP = false
	requireLogVolume = false
	testVolumeDriver(t, requireStaticIP, requireLogVolume)
	requireLogVolume = true
	testVolumeDriver(t, requireStaticIP, requireLogVolume)
}

func testVolumeDriver(t *testing.T, requireStaticIP bool, requireLogVolume bool) {
	flag.Parse()

	// create FireCampVolumeDriver
	dbIns := db.NewMemDB()
	mockDNS := dns.NewMockDNS()
	serverIns := server.NewLoopServer()
	mockServerInfo := server.NewMockServerInfo()
	contSvcIns := containersvc.NewMemContainerSvc()
	mockContInfo := containersvc.NewMockContainerSvcInfo()

	mgsvc := manageservice.NewManageService(dbIns, mockServerInfo, serverIns, mockDNS)

	driver := NewVolumeDriver(dbIns, mockDNS, serverIns, mockServerInfo, contSvcIns, mockContInfo)
	driver.ifname = "lo"

	defer cleanupStaticIP(requireStaticIP, driver.ifname, serverIns)

	requuid := utils.GenRequestUUID()
	ctx := context.Background()
	ctx = utils.NewRequestContext(ctx, requuid)

	cluster := "cluster1"
	taskCounts := 1
	region := "local-region"
	az := "local-az"
	domain := "test.com"
	vpcID := "vpc1"

	// create the 1st service
	service1 := "service1"

	// create the config files for replicas
	replicaCfgs := make([]*manage.ReplicaConfig, taskCounts)
	for i := 0; i < taskCounts; i++ {
		cfg := &manage.ReplicaConfigFile{FileName: "configfile-name", Content: "configfile-content"}
		configs := []*manage.ReplicaConfigFile{cfg}
		replicaCfg := &manage.ReplicaConfig{Zone: az, Configs: configs}
		replicaCfgs[i] = replicaCfg
	}

	req := &manage.CreateServiceRequest{
		Service: &manage.ServiceCommonRequest{
			Region:      region,
			Cluster:     cluster,
			ServiceName: service1,
		},
		Replicas: int64(taskCounts),
		Volume: &common.ServiceVolume{
			VolumeType:   common.VolumeTypeGPSSD,
			VolumeSizeGB: int64(taskCounts + 1),
		},
		RegisterDNS:     true,
		RequireStaticIP: requireStaticIP,
		ReplicaConfigs:  replicaCfgs,
	}
	if requireLogVolume {
		req.LogVolume = &common.ServiceVolume{
			VolumeType:   common.VolumeTypeGPSSD,
			VolumeSizeGB: 1,
			Iops:         1,
		}
	}

	uuid1, err := mgsvc.CreateService(ctx, req, domain, vpcID)
	if err != nil {
		t.Fatalf("CreateService error", err)
	}

	// create one task on the container instance
	task1 := "task1"
	err = contSvcIns.AddServiceTask(ctx, cluster, service1, task1, mockContInfo.GetLocalContainerInstanceID())
	if err != nil {
		t.Fatalf("AddServiceTask error", err)
	}

	volumeFuncTest(t, driver, uuid1)

	addSlot := false
	volumeMountTest(t, driver, uuid1, addSlot, requireLogVolume)

	// check the device is umounted.
	mountpath := driver.mountpoint(uuid1)
	if driver.isDeviceMountToPath(mountpath) {
		runlsblk()
		rundf()
		t.Fatalf("device is still mounted to %s", mountpath)
	}

	// test again with volume name as uuid+index
	addSlot = true
	volumeMountTest(t, driver, uuid1, addSlot, requireLogVolume)

	// create the 2nd service
	service2 := "service2"
	req.Service.ServiceName = service2
	uuid2, err := mgsvc.CreateService(ctx, req, domain, vpcID)
	if err != nil {
		t.Fatalf("CreateService error", err)
	}

	// create one task on the container instance
	task2 := "task2"
	err = contSvcIns.AddServiceTask(ctx, cluster, service2, task2, mockContInfo.GetLocalContainerInstanceID())
	if err != nil {
		t.Fatalf("AddServiceTask error", err)
	}

	members, err := dbIns.ListServiceMembers(ctx, uuid2)
	if err != nil || len(members) != 1 {
		t.Fatalf("ListServiceMembers error", err, "members", members)
	}
	member := members[0]
	member.ContainerInstanceID = mockContInfo.GetLocalContainerInstanceID()

	driver2 := NewVolumeDriver(dbIns, mockDNS, serverIns, mockServerInfo, contSvcIns, mockContInfo)
	driver.ifname = "lo"

	volumeMountTestWithDriverRestart(ctx, t, driver, driver2, uuid2, serverIns, member, requireLogVolume)

	// check the device is umounted.
	mountpath = driver.mountpoint(uuid2)
	if driver.isDeviceMountToPath(mountpath) {
		runlsblk()
		rundf()
		t.Fatalf("device is still mounted to %s", mountpath)
	}
}

func TestFindIdleVolume(t *testing.T) {
	// create FireCampVolumeDriver
	dbIns := db.NewMemDB()
	mockDNS := dns.NewMockDNS()
	serverIns := server.NewLoopServer()
	mockServerInfo := server.NewMockServerInfo()
	contSvcIns := containersvc.NewMemContainerSvc()
	mockContInfo := containersvc.NewMockContainerSvcInfo()

	requuid := utils.GenRequestUUID()
	ctx := context.Background()
	ctx = utils.NewRequestContext(ctx, requuid)

	driver := NewVolumeDriver(dbIns, mockDNS, serverIns, mockServerInfo, contSvcIns, mockContInfo)
	driver.ifname = "lo"

	cluster := "cluster1"
	service := "service1"
	serviceUUID := "service1-uuid"
	domain := "test.com"
	mtime := time.Now().UnixNano()
	replicas := int64(5)
	taskPrefix := "task-"
	contInsPrefix := "contins-"
	serverInsPrefix := "serverins-"
	volIDPrefix := "vol-"

	svols := common.ServiceVolumes{
		PrimaryDeviceName: "/dev/xvdf",
		PrimaryVolume: common.ServiceVolume{
			VolumeType:   common.VolumeTypeGPSSD,
			VolumeSizeGB: 1,
		},
	}
	sattr := db.CreateServiceAttr(serviceUUID, common.ServiceStatusActive, mtime, replicas, cluster, service, svols, true, domain, "hostedzone", false)

	// add 2 service tasks
	for i := 0; i < 2; i++ {
		str := strconv.Itoa(i)
		err := contSvcIns.AddServiceTask(ctx, cluster, service, taskPrefix+str, contInsPrefix+str)
		if err != nil {
			t.Fatalf("AddServiceTask error %s, index %d", err, i)
		}
	}

	// add 5 service members
	memNumber := 5
	for i := 0; i < memNumber; i++ {
		str := strconv.Itoa(i)
		mvols := common.MemberVolumes{
			PrimaryVolumeID:   volIDPrefix + str,
			PrimaryDeviceName: "/dev/xvdf",
		}
		m := db.CreateServiceMember(serviceUUID, utils.GenServiceMemberName(service, int64(i)),
			mockServerInfo.GetLocalAvailabilityZone(), taskPrefix+str, contInsPrefix+str,
			serverInsPrefix+str, mtime, mvols, common.DefaultHostIP, nil)
		err := dbIns.CreateServiceMember(ctx, m)
		if err != nil {
			t.Fatalf("CreateServiceMember error %s, index %d", err, i)
		}
	}

	// test selecting the idle member owned by local node
	str := strconv.Itoa(memNumber + 1)
	mvols := common.MemberVolumes{
		PrimaryVolumeID:   volIDPrefix + str,
		PrimaryDeviceName: "/dev/xvdf",
	}
	m := db.CreateServiceMember(serviceUUID, utils.GenServiceMemberName(service, int64(memNumber+1)),
		mockServerInfo.GetLocalAvailabilityZone(), taskPrefix+str, mockContInfo.GetLocalContainerInstanceID(),
		serverInsPrefix+str, mtime, mvols, common.DefaultHostIP, nil)
	err := dbIns.CreateServiceMember(ctx, m)
	if err != nil {
		t.Fatalf("CreateServiceMember error %s, index %d", err, memNumber+1)
	}
	m1, err := driver.findIdleMember(ctx, sattr, "requuid-1")
	if err != nil {
		t.Fatalf("findIdleMember error %s", err)
	}
	if !db.EqualServiceMember(m, m1, false) {
		t.Fatal("expect member %s, get %s", m, m1)
	}

	err = dbIns.DeleteServiceMember(ctx, serviceUUID, utils.GenServiceMemberName(service, int64(memNumber+1)))
	if err != nil {
		t.Fatalf("DeleteServiceMember error %s", err)
	}

	// select an idle member
	for j := 0; j < 3; j++ {
		m1, err = driver.findIdleMember(ctx, sattr, "requuid-1")
		if err != nil {
			t.Fatalf("findIdleMember error %s", err)
		}

		selected := false
		for i := 2; i < memNumber; i++ {
			str := strconv.Itoa(i)
			mvols := common.MemberVolumes{
				PrimaryVolumeID:   volIDPrefix + str,
				PrimaryDeviceName: "/dev/xvdf",
			}
			m := db.CreateServiceMember(serviceUUID, utils.GenServiceMemberName(service, int64(i)),
				mockServerInfo.GetLocalAvailabilityZone(), taskPrefix+str, contInsPrefix+str,
				serverInsPrefix+str, mtime, mvols, common.DefaultHostIP, nil)
			if db.EqualServiceMember(m, m1, false) {
				fmt.Println("select member", i)
				selected = true
			}
		}
		if !selected {
			t.Fatalf("not find member")
		}
	}
}

func TestVolumeInDifferentZone(t *testing.T) {
	requireStaticIP := true
	testVolumeInDifferentZone(t, requireStaticIP)

	requireStaticIP = false
	testVolumeInDifferentZone(t, requireStaticIP)
}

func testVolumeInDifferentZone(t *testing.T, requireStaticIP bool) {
	flag.Parse()

	// create FireCampVolumeDriver
	dbIns := db.NewMemDB()
	mockDNS := dns.NewMockDNS()
	serverIns := server.NewLoopServer()
	mockServerInfo := server.NewMockServerInfo()
	contSvcIns := containersvc.NewMemContainerSvc()
	mockContInfo := containersvc.NewMockContainerSvcInfo()

	requuid := utils.GenRequestUUID()
	ctx := context.Background()
	ctx = utils.NewRequestContext(ctx, requuid)

	mgsvc := manageservice.NewManageService(dbIns, mockServerInfo, serverIns, mockDNS)

	driver := NewVolumeDriver(dbIns, mockDNS, serverIns, mockServerInfo, contSvcIns, mockContInfo)
	driver.ifname = "lo"

	cluster := "cluster1"
	taskCounts := 1
	region := "local-region"
	az := "another-az"
	domain := "test.com"
	vpcID := "vpc1"

	// create the 1st service
	service1 := "service1"

	// create the config files for replicas
	replicaCfgs := make([]*manage.ReplicaConfig, taskCounts)
	for i := 0; i < taskCounts; i++ {
		cfg := &manage.ReplicaConfigFile{FileName: "configfile-name", Content: "configfile-content"}
		configs := []*manage.ReplicaConfigFile{cfg}
		replicaCfg := &manage.ReplicaConfig{Zone: az, Configs: configs}
		replicaCfgs[i] = replicaCfg
	}

	req := &manage.CreateServiceRequest{
		Service: &manage.ServiceCommonRequest{
			Region:      region,
			Cluster:     cluster,
			ServiceName: service1,
		},
		Replicas: int64(taskCounts),
		Volume: &common.ServiceVolume{
			VolumeType:   common.VolumeTypeGPSSD,
			VolumeSizeGB: int64(taskCounts + 1),
		},
		RegisterDNS:     true,
		RequireStaticIP: requireStaticIP,
		ReplicaConfigs:  replicaCfgs,
	}

	uuid1, err := mgsvc.CreateService(ctx, req, domain, vpcID)
	if err != nil {
		t.Fatalf("CreateService error", err)
	}

	// create one task on the container instance
	task1 := "task1"
	err = contSvcIns.AddServiceTask(ctx, cluster, service1, task1, mockContInfo.GetLocalContainerInstanceID())
	if err != nil {
		t.Fatalf("AddServiceTask error", err)
	}

	// mount the volume, expect fail as no volume at the same zone
	mreq := volume.MountRequest{Name: uuid1}
	mresp := driver.Mount(mreq)
	if len(mresp.Err) == 0 {
		t.Fatalf("expect error but mount volume succeed, service uuid", uuid1)
	}
}

func volumeFuncTest(t *testing.T, driver *FireCampVolumeDriver, svcuuid string) {
	path := filepath.Join(defaultRoot, svcuuid)

	r := volume.Request{Name: svcuuid}
	p := driver.Get(r)
	if len(p.Err) != 0 || p.Volume == nil || p.Volume.Name != svcuuid {
		t.Fatalf("Get expect volume name %s, get %v", svcuuid, p)
	}

	p = driver.Path(r)
	if len(p.Err) != 0 || p.Mountpoint != path {
		t.Fatalf("Get expect volume Mountpoint %s, get %v", path, p)
	}

	p = driver.Create(r)
	if len(p.Err) != 0 {
		t.Fatalf("Create expect success, get error %s", p.Err)
	}

	p = driver.Remove(r)
	if len(p.Err) != 0 {
		t.Fatalf("Remove expect success, get error %s", p.Err)
	}

	// negative case: non-exist service uuid
	r = volume.Request{Name: "non"}
	p = driver.Create(r)
	if len(p.Err) == 0 {
		t.Fatalf("Create succeeded, expect error")
	}

	p = driver.Remove(r)
	if len(p.Err) != 0 {
		t.Fatalf("Remove expect success, get error %s", p.Err)
	}

	// test serviceuuid-index
	svcslot := svcuuid + "-1"
	r = volume.Request{Name: svcslot}
	p = driver.Get(r)
	if len(p.Err) != 0 || p.Volume == nil || p.Volume.Name != svcslot {
		t.Fatalf("Get expect volume name %s, get %v", svcslot, p)
	}

	p = driver.Path(r)
	if len(p.Err) != 0 || p.Mountpoint != path {
		t.Fatalf("Get expect volume Mountpoint %s, get %v", path, p)
	}
}

func volumeMountTest(t *testing.T, driver *FireCampVolumeDriver, svcUUID string, addSlot bool, requireLogVolume bool) {
	name := svcUUID
	logName := common.LogDevicePathPrefix + common.NameSeparator + svcUUID
	if addSlot {
		name += "-0"
		logName += "-0"
	}
	mountpath := driver.mountpoint(svcUUID)
	logmountpath := driver.mountpoint(common.LogDevicePathPrefix + common.NameSeparator + svcUUID)

	// mount the volume
	mreq := volume.MountRequest{Name: name}
	mresp := driver.Mount(mreq)
	if len(mresp.Err) != 0 {
		t.Fatalf("failed to mount volume", name, "error", mresp.Err)
	}
	if requireLogVolume {
		mreq = volume.MountRequest{Name: logName}
		mresp = driver.Mount(mreq)
		if len(mresp.Err) != 0 {
			t.Fatalf("failed to mount volume", logName, "error", mresp.Err)
		}
	}

	expecterr := false
	// volume mounted, unmount before exit
	defer unmount(svcUUID, driver, t, expecterr, requireLogVolume)

	// check volume is mounted
	if !driver.isDeviceMountToPath(mountpath) {
		runlsblk()
		rundf()
		t.Fatalf("device is not mounted to %s", mountpath)
	}
	if requireLogVolume {
		if !driver.isDeviceMountToPath(logmountpath) {
			runlsblk()
			rundf()
			t.Fatalf("log device is not mounted to %s", logmountpath)
		}
	}

	// mount again to test the multiple mounts on the same volume
	mreq = volume.MountRequest{Name: name}
	mresp = driver.Mount(mreq)
	if len(mresp.Err) != 0 {
		t.Fatalf("failed to mount volume", svcUUID, "error", mresp.Err)
	}
	if requireLogVolume {
		mreq = volume.MountRequest{Name: logName}
		mresp = driver.Mount(mreq)
		if len(mresp.Err) != 0 {
			t.Fatalf("failed to mount volume", logName, "error", mresp.Err)
		}
	}

	// volume mounted, unmount before exit
	defer unmount(svcUUID, driver, t, expecterr, requireLogVolume)

	// check volume is mounted
	if !driver.isDeviceMountToPath(mountpath) {
		runlsblk()
		rundf()
		t.Fatalf("device is not mounted to %s", mountpath)
	}
	if requireLogVolume {
		if !driver.isDeviceMountToPath(logmountpath) {
			runlsblk()
			rundf()
			t.Fatalf("log device is not mounted to %s", logmountpath)
		}
	}

	// get volume
	req := volume.Request{Name: name}
	resp := driver.Get(req)
	if len(resp.Err) != 0 {
		t.Fatalf("failed to get volume", svcUUID, "error", resp.Err)
	}
	glog.Infoln("get volume", svcUUID, "resp", resp, resp.Volume)
	if resp.Volume.Mountpoint != mountpath {
		t.Fatalf("expect mount point %s, get %s", mountpath, resp.Volume.Mountpoint)
	}
	if requireLogVolume {
		logreq := volume.Request{Name: logName}
		resp = driver.Get(logreq)
		if len(resp.Err) != 0 {
			t.Fatalf("failed to get log volume", logName, "error", resp.Err)
		}
		glog.Infoln("get log volume", logName, "resp", resp, resp.Volume)
		if resp.Volume.Mountpoint != logmountpath {
			t.Fatalf("expect mount point %s, get %s", logmountpath, resp.Volume.Mountpoint)
		}
	}

	// list volume
	resp = driver.List(req)
	if len(resp.Err) != 0 {
		t.Fatalf("failed to list volumes, error", resp.Err)
	}
	glog.Infoln("list volumes", resp)

	// show misc info
	//runblkid("/dev/loop0")
	//rundf()
	//runlsblk()
}

func volumeMountTestWithDriverRestart(ctx context.Context, t *testing.T, driver *FireCampVolumeDriver,
	driver2 *FireCampVolumeDriver, svcUUID string, serverIns server.Server, member *common.ServiceMember, requireLogVolume bool) {
	name := svcUUID
	logName := common.LogDevicePathPrefix + common.NameSeparator + svcUUID

	// mount the volume
	mreq := volume.MountRequest{Name: name}
	mresp := driver.Mount(mreq)
	if len(mresp.Err) != 0 {
		t.Fatalf("failed to mount volume", name, "error", mresp.Err)
	}
	if requireLogVolume {
		mreq = volume.MountRequest{Name: logName}
		mresp = driver.Mount(mreq)
		if len(mresp.Err) != 0 {
			t.Fatalf("failed to mount volume", logName, "error", mresp.Err)
		}
	}

	expecterr := true
	// volume mounted, unmount before exit
	defer unmount(svcUUID, driver2, t, expecterr, requireLogVolume)

	// mount again to test the multiple mounts on the same volume
	mreq = volume.MountRequest{Name: name}
	mresp = driver.Mount(mreq)
	if len(mresp.Err) != 0 {
		t.Fatalf("failed to mount volume", name, "error", mresp.Err)
	}
	if requireLogVolume {
		mreq = volume.MountRequest{Name: logName}
		mresp = driver.Mount(mreq)
		if len(mresp.Err) != 0 {
			t.Fatalf("failed to mount volume", logName, "error", mresp.Err)
		}
	}

	// volume mounted, unmount before exit
	defer unmount(svcUUID, driver2, t, expecterr, requireLogVolume)
	defer driver2.detachVolumes(ctx, member, "requuid")

	// get volume
	req := volume.Request{Name: svcUUID}
	resp := driver.Get(req)
	if len(resp.Err) != 0 {
		t.Fatalf("failed to get volume", svcUUID, "error", resp.Err)
	}
	glog.Infoln("get volume", svcUUID, "resp", resp.Volume)

	// list volume
	resp = driver.List(req)
	if len(resp.Err) != 0 {
		t.Fatalf("failed to list volumes, error", resp.Err)
	}
	glog.Infoln("list volumes", resp.Volumes)

	// show misc info
	//runblkid("/dev/loop0")
	//rundf()
	//runlsblk()
}

func unmount(svcUUID string, driver *FireCampVolumeDriver, t *testing.T, expecterr bool, requireLogVolume bool) {
	if requireLogVolume {
		ureq := volume.UnmountRequest{Name: common.LogDevicePathPrefix + common.NameSeparator + svcUUID}
		uresp := driver.Unmount(ureq)
		if expecterr {
			if len(uresp.Err) == 0 {
				t.Fatalf("failed to unmount log volume", svcUUID, "error", uresp.Err)
			}
		} else {
			if len(uresp.Err) != 0 {
				t.Fatalf("failed to unmount log volume", svcUUID, "error", uresp.Err)
			}
		}
	}

	ureq := volume.UnmountRequest{Name: svcUUID}
	uresp := driver.Unmount(ureq)
	if expecterr {
		if len(uresp.Err) == 0 {
			t.Fatalf("failed to unmount primary volume", svcUUID, "error", uresp.Err)
		}
	} else {
		if len(uresp.Err) != 0 {
			t.Fatalf("failed to unmount primary volume", svcUUID, "error", uresp.Err)
		}
	}
}

func runblkid(dev string) {
	var args []string
	args = append(args, "blkid", dev)

	command := exec.Command(args[0], args[1:]...)

	output, err := command.CombinedOutput()
	glog.Infoln(args, "output", string(output[:]), "error", err)
}

func rundf() {
	var args []string
	args = append(args, "df", "-h")

	command := exec.Command(args[0], args[1:]...)

	output, err := command.CombinedOutput()
	glog.Errorln(args, "output", string(output[:]), "error", err)
}

func runlsblk() {
	var args []string
	args = append(args, "lsblk")

	command := exec.Command(args[0], args[1:]...)

	output, err := command.CombinedOutput()
	glog.Errorln(args, "output", string(output[:]), "error", err)
}

func cleanupStaticIP(requireStaticIP bool, ifname string, serverIns *server.LoopServer) {
	if requireStaticIP {
		cidrPrefix, _, _, _ := serverIns.GetCidrBlock()
		delIP(cidrPrefix, ifname)
	}
}
