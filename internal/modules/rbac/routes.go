package rbac

import "github.com/gin-gonic/gin"

func RegisterRoutes(r gin.IRouter, h *Handler, svc *Service) {
	r.GET("/permissions", svc.Require(PermRolesManage), h.ListPermissions)

	r.GET("/roles", svc.Require(PermRolesManage), h.ListRoles)
	r.POST("/roles", svc.Require(PermRolesManage), h.CreateRole)
	r.PUT("/roles/:id", svc.Require(PermRolesManage), h.UpdateRole)
	r.DELETE("/roles/:id", svc.Require(PermRolesManage), h.DeleteRole)

	r.GET("/users/:id/roles", svc.Require(PermUsersManage), h.GetUserRoles)
	r.PUT("/users/:id/roles", svc.Require(PermUsersManage), h.SetUserRoles)

	r.GET("/groups", svc.Require(PermUsersManage), h.ListGroups)
	r.POST("/groups", svc.Require(PermUsersManage), h.CreateGroup)
	r.DELETE("/groups/:id", svc.Require(PermUsersManage), h.DeleteGroup)
	r.GET("/groups/:id/members", svc.Require(PermUsersManage), h.ListGroupMembers)
	r.POST("/groups/:id/members", svc.Require(PermUsersManage), h.AddGroupMember)
	r.DELETE("/groups/:id/members/:userId", svc.Require(PermUsersManage), h.RemoveGroupMember)

	r.GET("/grants", svc.Require(PermPermissionsManage), h.ListGrants)
	r.POST("/grants", svc.Require(PermPermissionsManage), h.CreateGrant)
	r.DELETE("/grants/:id", svc.Require(PermPermissionsManage), h.DeleteGrant)
}
