package web

import (
	"context"
	"crypto/tls"
	"embed"
	"fmt"
	"html/template"
	"io"
	"io/fs"
	"net"
	"net/http"
	"os"
	"strconv"
	"strings"
	"time"

	"github.com/Hhz0823/1s-ui/api"
	"github.com/Hhz0823/1s-ui/config"
	"github.com/Hhz0823/1s-ui/logger"
	"github.com/Hhz0823/1s-ui/middleware"
	"github.com/Hhz0823/1s-ui/network"
	"github.com/Hhz0823/1s-ui/service"

	"github.com/gin-contrib/gzip"
	"github.com/gin-contrib/sessions"
	"github.com/gin-contrib/sessions/cookie"
	"github.com/gin-gonic/gin"
)

//go:embed *
var content embed.FS

type Server struct {
	httpServer        *http.Server
	listener          net.Listener
	controlServer     *http.Server
	controlListener   net.Listener
	controlSocketPath string
	ctx               context.Context
	cancel            context.CancelFunc
	settingService    service.SettingService
}

func NewServer() *Server {
	ctx, cancel := context.WithCancel(context.Background())
	return &Server{
		ctx:    ctx,
		cancel: cancel,
	}
}

func (s *Server) initRouter() (*gin.Engine, error) {
	if config.IsDebug() {
		gin.SetMode(gin.DebugMode)
	} else {
		gin.DefaultWriter = io.Discard
		gin.DefaultErrorWriter = io.Discard
		gin.SetMode(gin.ReleaseMode)
	}

	engine := gin.Default()

	webFS, htmlPattern, assetsFS, dynamicIndex, err := getWebFiles()
	if err != nil {
		return nil, err
	}

	// Load the HTML template
	t := template.New("").Funcs(engine.FuncMap)
	template, err := t.ParseFS(webFS, htmlPattern)
	if err != nil {
		return nil, err
	}
	engine.SetHTMLTemplate(template)

	base_url, err := s.settingService.GetWebPath()
	if err != nil {
		return nil, err
	}

	webDomain, err := s.settingService.GetWebDomain()
	if err != nil {
		return nil, err
	}

	if webDomain != "" {
		engine.Use(middleware.DomainValidator(webDomain))
	}

	secret, err := s.settingService.GetSecret()
	if err != nil {
		return nil, err
	}

	engine.Use(gzip.Gzip(gzip.DefaultCompression))
	assetsBasePath := base_url + "assets/"

	store := cookie.NewStore(secret)
	engine.Use(sessions.Sessions("s-ui", store))

	engine.Use(func(c *gin.Context) {
		uri := c.Request.RequestURI
		if strings.HasPrefix(uri, assetsBasePath) {
			c.Header("Cache-Control", "max-age=31536000")
		}
	})

	// Serve the assets folder
	engine.StaticFS(assetsBasePath, http.FS(assetsFS))

	// Refresh links are bearer URLs: possession of the per-item random token
	// authorizes rotating only that relay item's IPv6 address.
	engine.GET(base_url+"refresh/:token", func(c *gin.Context) {
		c.Header("Cache-Control", "no-store")
		var relayService service.ConfigService
		result, rotateErr := relayService.RotateRelayItemByToken(c.Param("token"), "refresh-link:"+c.ClientIP())
		if rotateErr != nil {
			status := http.StatusServiceUnavailable
			if strings.Contains(rotateErr.Error(), "not found") || strings.Contains(rotateErr.Error(), "invalid relay refresh token") {
				status = http.StatusNotFound
			}
			c.String(status, "IPv6 rotation failed: %s\n", rotateErr.Error())
			return
		}
		address := ""
		if len(result.IPv6) > 0 {
			address = result.IPv6[0]
		}
		c.String(http.StatusOK, "success\nitem=%d\nipv6=%s\n", result.ItemIndex, address)
	})

	group_apiv2 := engine.Group(base_url + "apiv2")
	apiv2 := api.NewAPIv2Handler(group_apiv2)

	group_agent := engine.Group(base_url + "agent/v1")
	api.NewAgentHandler(group_agent)

	group_api := engine.Group(base_url + "api")
	api.NewAPIHandler(group_api, apiv2)

	// Serve index.html as the entry point
	// Handle all other routes by serving index.html
	engine.NoRoute(func(c *gin.Context) {
		if c.Request.URL.Path == strings.TrimSuffix(base_url, "/") {
			c.Redirect(http.StatusTemporaryRedirect, base_url)
			return
		}
		if !strings.HasPrefix(c.Request.URL.Path, base_url) {
			c.String(404, "")
			return
		}
		if strings.HasPrefix(c.Request.URL.Path, assetsBasePath) {
			c.Status(http.StatusNotFound)
			return
		}
		if c.Request.URL.Path != base_url+"login" && !api.IsLogin(c) {
			c.Redirect(http.StatusTemporaryRedirect, base_url+"login")
			return
		}
		if c.Request.URL.Path == base_url+"login" && api.IsLogin(c) {
			c.Redirect(http.StatusTemporaryRedirect, base_url)
			return
		}
		setIndexNoCache(c)
		if dynamicIndex {
			renderDynamicIndex(c, webFS, htmlPattern, base_url)
			return
		}
		c.HTML(http.StatusOK, "index.html", gin.H{"BASE_URL": base_url})
	})

	return engine, nil
}

func renderDynamicIndex(c *gin.Context, webFS fs.FS, htmlPattern, baseURL string) {
	t, err := template.New("").ParseFS(webFS, htmlPattern)
	if err != nil {
		logger.Error("load web UI index failed: ", err)
		c.String(http.StatusInternalServerError, "web UI is temporarily unavailable")
		return
	}
	c.Header("Content-Type", "text/html; charset=utf-8")
	c.Status(http.StatusOK)
	if err := t.ExecuteTemplate(c.Writer, "index.html", gin.H{"BASE_URL": baseURL}); err != nil {
		logger.Error("render web UI index failed: ", err)
	}
}

func setIndexNoCache(c *gin.Context) {
	c.Header("Cache-Control", "no-store, no-cache, must-revalidate, max-age=0")
	c.Header("Pragma", "no-cache")
	c.Header("Expires", "0")
}

func getWebFiles() (fs.FS, string, fs.FS, bool, error) {
	if _, err := fs.Stat(content, "html/index.html"); err == nil {
		assetsFS, err := fs.Sub(content, "html/assets")
		return content, "html/index.html", assetsFS, false, err
	}

	if _, err := os.Stat("frontend/dist/index.html"); err == nil {
		logger.Info("embedded web UI not found, using frontend/dist")
		return os.DirFS("frontend/dist"), "index.html", os.DirFS("frontend/dist/assets"), true, nil
	}

	return nil, "", nil, false, fmt.Errorf("web UI assets not found, run `cd frontend && npm run build` or `./build.sh` first")
}

func (s *Server) Start() (err error) {
	//This is an anonymous function, no function name
	defer func() {
		if err != nil {
			s.Stop()
		}
	}()

	engine, err := s.initRouter()
	if err != nil {
		return err
	}

	certFile, err := s.settingService.GetCertFile()
	if err != nil {
		return err
	}
	keyFile, err := s.settingService.GetKeyFile()
	if err != nil {
		return err
	}
	listen, err := s.settingService.GetListen()
	if err != nil {
		return err
	}
	port, err := s.settingService.GetPort()
	if err != nil {
		return err
	}
	listenAddr := net.JoinHostPort(listen, strconv.Itoa(port))
	listener, err := net.Listen("tcp", listenAddr)
	if err != nil {
		return err
	}
	if certFile != "" || keyFile != "" {
		cert, err := tls.LoadX509KeyPair(certFile, keyFile)
		if err != nil {
			listener.Close()
			return err
		}
		c := &tls.Config{
			Certificates: []tls.Certificate{cert},
		}
		listener = network.NewAutoHttpsListener(listener)
		listener = tls.NewListener(listener, c)
	}

	if certFile != "" || keyFile != "" {
		logger.Info("web server run https on", listener.Addr())
	} else {
		logger.Info("web server run http on", listener.Addr())
	}
	s.listener = listener

	s.httpServer = &http.Server{
		Handler: engine,
	}
	if err := s.startControlSocket(); err != nil {
		logger.Warning("local managed-node control is unavailable: ", err)
	}

	go func() {
		s.httpServer.Serve(listener)
	}()

	return err
}

func (s *Server) Stop() error {
	var err error
	controlErr := s.stopControlSocket()
	if s.httpServer != nil {
		shutdownCtx, cancelShutdown := context.WithTimeout(context.Background(), 30*time.Second)
		err = s.httpServer.Shutdown(shutdownCtx)
		cancelShutdown()
		if err != nil {
			s.cancel()
			if s.listener != nil {
				_ = s.listener.Close()
			}
			return err
		}
	} else if s.listener != nil {
		err = s.listener.Close()
		if err != nil {
			s.cancel()
			return err
		}
	}
	s.cancel()
	if err == nil {
		err = controlErr
	}
	return err
}

func (s *Server) GetCtx() context.Context {
	return s.ctx
}
