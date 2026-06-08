package main

import internalterminal "github.com/oines/astralops/daemon/internal/core/terminal"

func (a *app) terminalManager() *internalterminal.Manager {
	service := a.terminalService()
	delegate, _ := service.Delegate().(*terminalCoreDelegate)
	if delegate == nil {
		return nil
	}
	return delegate.manager
}
