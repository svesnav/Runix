package servers

import "github.com/gin-gonic/gin"

func RegisterRoutes(r gin.IRouter, h *Handler, requirePerm, requireServerPerm func(string) gin.HandlerFunc) {
	r.GET("/servers", requirePerm("server.view"), h.List)
	r.POST("/servers", requirePerm("server.create"), h.Create)
	r.GET("/servers/:id", requireServerPerm("server.view"), h.Get)
	r.PUT("/servers/:id", requireServerPerm("server.update"), h.Update)
	r.DELETE("/servers/:id", requireServerPerm("server.delete"), h.Delete)
	r.POST("/servers/:id/token/rotate", requireServerPerm("server.update"), h.RotateToken)

	r.GET("/server-groups", requirePerm("server.view"), h.ListGroups)
	r.POST("/server-groups", requirePerm("server.create"), h.CreateGroup)
	r.DELETE("/server-groups/:id", requirePerm("server.delete"), h.DeleteGroup)
	r.GET("/server-groups/:id/members", requirePerm("server.view"), h.ListGroupMembers)
	r.POST("/server-groups/:id/members", requirePerm("server.update"), h.AddGroupMember)
	r.DELETE("/server-groups/:id/members/:serverId", requirePerm("server.update"), h.RemoveGroupMember)
}
