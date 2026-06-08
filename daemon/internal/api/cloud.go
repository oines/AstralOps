package api

import (
	"net/http"

	"github.com/oines/astralops/daemon/internal/ports"
)

type CloudHandler struct {
	Cloud ports.CloudCommands
}

func NewCloudHandler(cloud ports.CloudCommands) *CloudHandler {
	return &CloudHandler{Cloud: cloud}
}

func (h *CloudHandler) HandleCloudAuthAction(w http.ResponseWriter, r *http.Request) {
	h.Cloud.ServeCloudAuthAction(w, r)
}

func (h *CloudHandler) HandleCloudAccount(w http.ResponseWriter, r *http.Request) {
	h.Cloud.ServeCloudAccount(w, r)
}

func (h *CloudHandler) HandleCloudAccountRelay(w http.ResponseWriter, r *http.Request) {
	h.Cloud.ServeCloudAccountRelay(w, r)
}

func (h *CloudHandler) HandleCloudRelays(w http.ResponseWriter, r *http.Request) {
	h.Cloud.ServeCloudRelays(w, r)
}

func (h *CloudHandler) HandleCloudDevices(w http.ResponseWriter, r *http.Request) {
	h.Cloud.ServeCloudDevices(w, r)
}

func (h *CloudHandler) HandleCloudDeviceAction(w http.ResponseWriter, r *http.Request) {
	h.Cloud.ServeCloudDeviceAction(w, r)
}

func (h *CloudHandler) HandleCloudHeartbeat(w http.ResponseWriter, r *http.Request) {
	h.Cloud.ServeCloudHeartbeat(w, r)
}

func (h *CloudHandler) HandleCloudPairingRequests(w http.ResponseWriter, r *http.Request) {
	h.Cloud.ServeCloudPairingRequests(w, r)
}

func (h *CloudHandler) HandleCloudPairingRequestAction(w http.ResponseWriter, r *http.Request) {
	h.Cloud.ServeCloudPairingRequestAction(w, r)
}
