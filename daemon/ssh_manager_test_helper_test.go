package main

func (a *app) setSSHManagerForTest(manager *sshManager) {
	service := a.sshService()
	delegate, _ := service.Delegate().(*sshCoreDelegate)
	if delegate == nil {
		return
	}
	delegate.manager = manager
	if delegate.manager != nil {
		delegate.manager.UpdateDeps(sshDepsFromApp(a))
	}
}

func (a *app) sshManagerForTest() *sshManager {
	service := a.sshService()
	delegate, _ := service.Delegate().(*sshCoreDelegate)
	if delegate == nil {
		return nil
	}
	return delegate.manager
}

func (a *app) seedConnectedSSHProxyForTest(workspace Workspace, proxy *proxyClient) {
	manager := a.sshManagerForTest()
	if manager == nil {
		manager = newSSHManager(a)
		a.setSSHManagerForTest(manager)
	}
	manager.SeedConnectedProxyForTest(workspace, proxy, initialSSHConnection(workspace, connectionConnected))
}
