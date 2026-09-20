package api

import (
	"strings"

	"github.com/Hhz0823/1s-ui/util/common"

	"github.com/gin-gonic/gin"
)

type APIHandler struct {
	ApiService
	apiv2 *APIv2Handler
}

func NewAPIHandler(g *gin.RouterGroup, a2 *APIv2Handler) {
	a := &APIHandler{
		apiv2: a2,
	}
	a.initRouter(g)
}

func (a *APIHandler) initRouter(g *gin.RouterGroup) {
	g.Use(func(c *gin.Context) {
		path := c.Request.URL.Path
		if !strings.HasSuffix(path, "login") && !strings.HasSuffix(path, "logout") {
			checkLogin(c)
		}
	})
	g.POST("/:postAction", a.postHandler)
	g.POST("/relay/:id/delete", func(c *gin.Context) { a.ApiService.DeleteRelay(c, GetLoginUser(c)) })
	g.POST("/relay/:id/rotate", func(c *gin.Context) { a.ApiService.RotateRelayPool(c, GetLoginUser(c)) })
	g.POST("/relay/:id/rotation", func(c *gin.Context) { a.ApiService.SetRelayPoolRotation(c, GetLoginUser(c)) })
	g.POST("/relay/create", func(c *gin.Context) { a.ApiService.CreateRelay(c, GetLoginUser(c)) })
	g.GET("/relay/:id/bitbrowser.xlsx", a.ApiService.ExportRelayBitBrowser)
	g.GET("/agents", a.ApiService.GetAgents)
	g.GET("/agents/commands", a.ApiService.ListAgentCommands)
	g.POST("/agents/batch-command", a.ApiService.ControlAgentsBatch)
	g.GET("/agents/:id", a.ApiService.GetAgent)
	g.PATCH("/agents/:id", a.ApiService.UpdateAgent)
	g.GET("/agents/:id/inbounds", a.ApiService.GetAgentInbounds)
	g.GET("/agents/:id/inbounds/editor", a.ApiService.GetAgentInboundEditor)
	g.POST("/agents/:id/inbounds/save", a.ApiService.SaveAgentInbound)
	g.POST("/agents/:id/inbounds/quick-add", a.ApiService.QuickAddAgentInbounds)
	g.GET("/agents/:id/relay", a.ApiService.GetAgentRelayData)
	g.POST("/agents/:id/relay/create", a.ApiService.CreateAgentRelay)
	g.POST("/agents/:id/relay/:relayId/delete", a.ApiService.DeleteAgentRelay)
	g.POST("/agents/:id/relay/:relayId/rotate", a.ApiService.RotateAgentRelayPool)
	g.POST("/agents/:id/relay/:relayId/rotation", a.ApiService.SetAgentRelayPoolRotation)
	g.GET("/agents/:id/relay/:relayId/bitbrowser.xlsx", a.ApiService.ExportAgentRelayBitBrowser)
	g.GET("/agents/:id/terminal", a.ApiService.AgentTerminal)
	g.POST("/agents", a.ApiService.CreateAgent)
	g.POST("/agents/enrollment-link", a.ApiService.CreateAgentEnrollmentLink)
	g.POST("/agents/connect-local", a.ApiService.ConnectLocalAgent)
	g.POST("/agents/:id/command", a.ApiService.ControlAgent)
	g.POST("/agents/:id/rotate", a.ApiService.RotateAgent)
	g.POST("/agents/:id/delete", a.ApiService.DeleteAgent)
	g.GET("/reverse-proxy", a.ApiService.GetReverseProxy)
	g.POST("/reverse-proxy", a.ApiService.SetReverseProxy)
	g.GET("/:getAction", a.getHandler)
}

func (a *APIHandler) postHandler(c *gin.Context) {
	loginUser := GetLoginUser(c)
	action := c.Param("postAction")

	switch action {
	case "login":
		a.ApiService.Login(c)
	case "changePass":
		a.ApiService.ChangePass(c)
	case "save":
		a.ApiService.Save(c, loginUser)
	case "restartApp":
		a.ApiService.RestartApp(c)
	case "restartSb":
		a.ApiService.RestartSb(c)
	case "resetTraffic":
		a.ApiService.ResetTraffic(c)
	case "restartXray":
		a.ApiService.RestartXray(c)
	case "linkConvert":
		a.ApiService.LinkConvert(c)
	case "subConvert":
		a.ApiService.SubConvert(c)
	case "importdb":
		a.ApiService.ImportDb(c)
	case "addToken":
		a.ApiService.AddToken(c)
		a.apiv2.ReloadTokens()
	case "deleteToken":
		a.ApiService.DeleteToken(c)
		a.apiv2.ReloadTokens()
	case "setSysctl":
		a.ApiService.SetSysctl(c)
	case "pinnedSha256":
		a.ApiService.PinnedSha256(c)
	default:
		jsonMsg(c, "failed", common.NewError("unknown action: ", action))
	}
}

func (a *APIHandler) getHandler(c *gin.Context) {
	action := c.Param("getAction")

	switch action {
	case "logout":
		a.ApiService.Logout(c)
	case "load":
		a.ApiService.LoadData(c)
	case "inbounds", "outbounds", "endpoints", "services", "tls", "clients", "config":
		err := a.ApiService.LoadPartialData(c, []string{action})
		if err != nil {
			jsonMsg(c, action, err)
		}
		return
	case "users":
		a.ApiService.GetUsers(c)
	case "settings":
		a.ApiService.GetSettings(c)
	case "stats":
		a.ApiService.GetStats(c)
	case "status":
		a.ApiService.GetStatus(c)
	case "onlines":
		a.ApiService.GetOnlines(c)
	case "logs":
		a.ApiService.GetLogs(c)
	case "changes":
		a.ApiService.CheckChanges(c)
	case "keypairs":
		a.ApiService.GetKeypairs(c)
	case "getdb":
		a.ApiService.GetDb(c)
	case "tokens":
		a.ApiService.GetTokens(c)
	case "singbox-config":
		a.ApiService.GetSingboxConfig(c)
	case "xray-config":
		a.ApiService.GetXrayConfig(c)
	case "checkXray":
		a.ApiService.GetCheckXray(c)
	case "checkOutbound":
		a.ApiService.GetCheckOutbound(c)
	case "checkWarp":
		a.ApiService.GetCheckWarp(c)
	case "relay":
		a.ApiService.GetRelayData(c)
	default:
		jsonMsg(c, "failed", common.NewError("unknown action: ", action))
	}
}
