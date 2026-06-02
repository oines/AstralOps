package main

import "testing"

func TestAppRoleDefaultsToDesktop(t *testing.T) {
	app := &app{}
	if app.currentRole() != appRoleDesktop {
		t.Fatalf("role = %q, want desktop", app.currentRole())
	}
	if !app.hostRoleEnabled() || !app.controllerRoleEnabled() {
		t.Fatalf("desktop role should enable host and controller")
	}
}

func TestAppRoleHostOnlyDisablesControllerCore(t *testing.T) {
	app := &app{role: appRoleHost}
	if !app.hostRoleEnabled() || app.controllerRoleEnabled() {
		t.Fatalf("host role host=%v controller=%v", app.hostRoleEnabled(), app.controllerRoleEnabled())
	}
	if app.controllerCoreManager() != nil {
		t.Fatal("host-only role initialized controller core")
	}
}

func TestAppRoleControllerOnlyDisablesHostCore(t *testing.T) {
	app := &app{role: appRoleController}
	if app.hostRoleEnabled() || !app.controllerRoleEnabled() {
		t.Fatalf("controller role host=%v controller=%v", app.hostRoleEnabled(), app.controllerRoleEnabled())
	}
	if app.hostCoreManager() != nil {
		t.Fatal("controller-only role initialized host core")
	}
}
