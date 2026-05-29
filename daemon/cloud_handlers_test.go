package main

import (
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/oines/astralops/internal/cloudbroker"
)

func TestCloudHandlersRegisterAndListCurrentDevice(t *testing.T) {
	app, broker := testCloudApp(t)
	defer broker.Close()

	registerReq := httptest.NewRequest(http.MethodPost, "/v1/cloud/devices", strings.NewReader(`{"can_host":true,"can_control":true}`))
	registerRR := httptest.NewRecorder()
	app.handleCloudDevices(registerRR, registerReq)
	if registerRR.Code != http.StatusOK {
		t.Fatalf("register status = %d body=%s", registerRR.Code, registerRR.Body.String())
	}
	var registered CloudDeviceRecord
	if err := json.Unmarshal(registerRR.Body.Bytes(), &registered); err != nil {
		t.Fatal(err)
	}
	if registered.DeviceID != app.store.hostInfo().Identity.DeviceID || !registered.CanHost || !registered.CanControl {
		t.Fatalf("registered = %#v", registered)
	}

	listReq := httptest.NewRequest(http.MethodGet, "/v1/cloud/devices", nil)
	listRR := httptest.NewRecorder()
	app.handleCloudDevices(listRR, listReq)
	if listRR.Code != http.StatusOK {
		t.Fatalf("list status = %d body=%s", listRR.Code, listRR.Body.String())
	}
	var list cloudDeviceListResponse
	if err := json.Unmarshal(listRR.Body.Bytes(), &list); err != nil {
		t.Fatal(err)
	}
	if len(list.Devices) != 1 || list.Devices[0].DeviceID != registered.DeviceID {
		t.Fatalf("devices = %#v", list.Devices)
	}
}

func TestCloudHandlersPairingSignalDoesNotWriteLocalTrust(t *testing.T) {
	app, broker := testCloudApp(t)
	defer broker.Close()

	registerRR := httptest.NewRecorder()
	app.handleCloudDevices(registerRR, httptest.NewRequest(http.MethodPost, "/v1/cloud/devices", strings.NewReader(`{"can_host":true,"can_control":true}`)))
	if registerRR.Code != http.StatusOK {
		t.Fatalf("register host status = %d body=%s", registerRR.Code, registerRR.Body.String())
	}
	controller := testControllerRegistration(t, "dev_phone")
	res, err := httpClientPostJSON(broker.URL+"/v1/devices", "account-token", controller)
	if err != nil {
		t.Fatal(err)
	}
	_ = res.Body.Close()
	if res.StatusCode != http.StatusOK {
		t.Fatalf("register controller status = %d", res.StatusCode)
	}

	hostID := app.store.hostInfo().Identity.DeviceID
	body := `{"host_device_id":"` + hostID + `","controller_device_id":"dev_phone","scope":"full"}`
	pairRR := httptest.NewRecorder()
	app.handleCloudPairingRequests(pairRR, httptest.NewRequest(http.MethodPost, "/v1/cloud/pairing/requests", strings.NewReader(body)))
	if pairRR.Code != http.StatusAccepted {
		t.Fatalf("pair status = %d body=%s", pairRR.Code, pairRR.Body.String())
	}
	var created cloudPairingSignalResponse
	if err := json.Unmarshal(pairRR.Body.Bytes(), &created); err != nil {
		t.Fatal(err)
	}

	resolveRR := httptest.NewRecorder()
	resolvePath := "/v1/cloud/pairing/requests/" + created.Request.RequestID + "/resolve"
	app.handleCloudPairingRequestAction(resolveRR, httptest.NewRequest(http.MethodPost, resolvePath, strings.NewReader(`{"status":"approved","resolver_device_id":"`+hostID+`"}`)))
	if resolveRR.Code != http.StatusOK {
		t.Fatalf("resolve status = %d body=%s", resolveRR.Code, resolveRR.Body.String())
	}
	if _, ok := app.store.trustedControlGrant("dev_phone"); ok {
		t.Fatal("cloud pairing resolve wrote local trust; cloud signal must not grant Host access")
	}
}

func testCloudApp(t *testing.T) (*app, *httptest.Server) {
	t.Helper()
	cloudStore, err := cloudbroker.LoadFileStore(t.TempDir() + "/cloud.json")
	if err != nil {
		t.Fatal(err)
	}
	broker := httptest.NewServer(cloudbroker.NewServer(cloudStore, []string{"account-token"}).Handler())
	dir := t.TempDir()
	st, err := loadStore(dir)
	if err != nil {
		t.Fatal(err)
	}
	settings, err := loadSettingsStore(dir)
	if err != nil {
		t.Fatal(err)
	}
	enabled := true
	baseURL := broker.URL
	token := "account-token"
	if _, err := settings.patch(appSettingsPatch{Cloud: &cloudSettingsPatch{Enabled: &enabled, BaseURL: &baseURL, AccountToken: &token}}); err != nil {
		t.Fatal(err)
	}
	return &app{store: st, settings: settings}, broker
}

func testControllerRegistration(t *testing.T, deviceID string) cloudbroker.DeviceRegistration {
	t.Helper()
	st, err := loadStore(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	identity := st.hostInfo().Identity
	identity.DeviceID = deviceID
	identity.DeviceKind = "mobile"
	identity.DeviceName = "phone"
	return cloudbroker.DeviceRegistration{
		DeviceID:             identity.DeviceID,
		DeviceName:           identity.DeviceName,
		DeviceKind:           identity.DeviceKind,
		PublicKey:            identity.PublicKey,
		PublicKeyFingerprint: identity.PublicKeyFingerprint,
		Capabilities:         []string{CapabilityCoreRead},
		CanHost:              false,
		CanControl:           true,
	}
}

func httpClientPostJSON(url, token string, body any) (*http.Response, error) {
	payload, err := json.Marshal(body)
	if err != nil {
		return nil, err
	}
	req, err := http.NewRequest(http.MethodPost, url, strings.NewReader(string(payload)))
	if err != nil {
		return nil, err
	}
	req.Header.Set("Authorization", "Bearer "+token)
	req.Header.Set("Content-Type", "application/json")
	return http.DefaultClient.Do(req)
}
