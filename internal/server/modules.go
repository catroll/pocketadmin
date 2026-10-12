package server

import "github.com/gin-gonic/gin"

// registerModuleRoutes mounts the identity, organization and rbac modules.
//
// Every route goes through safe(), guarded() or superuserGuarded() so that the
// startup self-check can prove no state-changing route was left unprotected.
func (s *Server) registerModuleRoutes(panel *gin.RouterGroup) {
	s.registerIdentityRoutes(panel)
	s.registerStorageRoutes(panel)
	s.registerAuditRoutes(panel)
	s.registerSettingsRoutes(panel)
	s.registerJobRoutes(panel)
	s.registerAnnouncementRoutes(panel)
	s.registerWorkbenchRoutes(panel)
	s.registerDeveloperRoutes(panel)
	s.registerIntegrationRoutes(panel)
	s.registerMonitoringRoutes(panel)
	s.registerCatalogRoutes(panel)
	s.registerMembershipRoutes(panel)
	s.registerPromotionRoutes(panel)
	s.registerSubscriptionRoutes(panel)
	s.registerOrderRoutes(panel)
	s.registerPaymentRoutes(panel)
	s.registerOrganizationRoutes(panel)
	s.registerRBACRoutes(panel)
}

// registerIdentityRoutes mounts the account, session and sign-in history screens.
func (s *Server) registerIdentityRoutes(panel *gin.RouterGroup) {
	for _, r := range identityResources() {
		s.resourceRoutes(panel, r)
	}
	s.registerAccountRoutes(panel)
	s.registerAccountActionRoutes(panel)
	s.registerImpersonationRoutes(panel)
	s.registerPreferenceRoutes(panel)
	s.registerMFAManagementRoutes(panel)
}

// registerOrganizationRoutes mounts the department, position and staff screens.
func (s *Server) registerOrganizationRoutes(panel *gin.RouterGroup) {
	for _, r := range organizationResources() {
		s.resourceRoutes(panel, r)
	}
	s.registerOrgChartRoutes(panel)
}

// registerRBACRoutes mounts the role, permission, menu and dictionary screens.
func (s *Server) registerRBACRoutes(panel *gin.RouterGroup) {
	for _, r := range rbacResources() {
		s.resourceRoutes(panel, r)
	}
}
