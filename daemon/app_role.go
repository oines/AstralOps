package main

import "strings"

type appRole string

const (
	appRoleDesktop    appRole = "desktop"
	appRoleHost       appRole = "host"
	appRoleController appRole = "controller"
)

func normalizeAppRole(role string) appRole {
	switch appRole(strings.TrimSpace(role)) {
	case appRoleHost:
		return appRoleHost
	case appRoleController:
		return appRoleController
	default:
		return appRoleDesktop
	}
}

func (a *app) currentRole() appRole {
	if a == nil || a.role == "" {
		return appRoleDesktop
	}
	return normalizeAppRole(string(a.role))
}

func (a *app) hostRoleEnabled() bool {
	return a.currentRole() != appRoleController
}

func (a *app) controllerRoleEnabled() bool {
	return a.currentRole() != appRoleHost
}
