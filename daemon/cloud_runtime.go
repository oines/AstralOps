package main

import (
	"context"
	"log"
	"time"
)

const (
	cloudSyncInterval = 60 * time.Second
	cloudSyncTimeout  = 15 * time.Second
)

func (a *app) applyCloudSettings(settings CloudSettings) error {
	settings = normalizedAppSettings(AppSettings{Cloud: settings}).Cloud
	if settings.Enabled {
		if err := validateCloudSettings(settings); err != nil {
			return err
		}
	}

	a.cloudMu.Lock()
	defer a.cloudMu.Unlock()
	if a.cloudCancel != nil {
		a.cloudCancel()
		a.cloudCancel = nil
	}
	a.cloudSettings = settings
	if !settings.Enabled {
		return nil
	}

	ctx, cancel := context.WithCancel(context.Background())
	a.cloudCancel = cancel
	go a.cloudSyncLoop(ctx, settings)
	return nil
}

func (a *app) cloudSyncLoop(ctx context.Context, settings CloudSettings) {
	client := CloudClient{BaseURL: settings.BaseURL, Token: settings.AccountToken}
	if err := a.cloudRegisterAndHeartbeat(ctx, client); err != nil {
		log.Printf("astralops cloud sync: %v", err)
	}

	ticker := time.NewTicker(cloudSyncInterval)
	defer ticker.Stop()
	defer a.cloudMarkOffline(settings)

	for {
		select {
		case <-ctx.Done():
			return
		case <-ticker.C:
			if err := a.cloudRegisterAndHeartbeat(ctx, client); err != nil {
				log.Printf("astralops cloud sync: %v", err)
			}
		}
	}
}

func (a *app) cloudRegisterAndHeartbeat(ctx context.Context, client CloudClient) error {
	if a == nil || a.store == nil {
		return nil
	}
	registerCtx, cancel := context.WithTimeout(ctx, cloudSyncTimeout)
	defer cancel()
	settings := a.currentSettings()
	if _, err := client.RegisterDevice(registerCtx, a.store.hostInfo().Identity, settings.RemoteControl.Enabled, true, ""); err != nil {
		return err
	}

	heartbeatCtx, heartbeatCancel := context.WithTimeout(ctx, cloudSyncTimeout)
	defer heartbeatCancel()
	if _, err := client.HeartbeatDevice(heartbeatCtx, a.store.hostInfo().Identity.DeviceID, ""); err != nil {
		return err
	}
	return nil
}

func (a *app) cloudMarkOffline(settings CloudSettings) {
	if a == nil || a.store == nil || !settings.Enabled {
		return
	}
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	client := CloudClient{BaseURL: settings.BaseURL, Token: settings.AccountToken}
	if _, err := client.MarkDeviceOffline(ctx, a.store.hostInfo().Identity.DeviceID); err != nil {
		log.Printf("astralops cloud offline: %v", err)
	}
}

func cloudSettingsChanged(left, right CloudSettings) bool {
	left = normalizedAppSettings(AppSettings{Cloud: left}).Cloud
	right = normalizedAppSettings(AppSettings{Cloud: right}).Cloud
	return left.Enabled != right.Enabled || left.BaseURL != right.BaseURL || left.AccountToken != right.AccountToken
}
