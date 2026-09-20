package service

import (
	"encoding/base64"
	"os"
	"runtime"
	"strconv"
	"strings"
	"sync"
	"time"

	"github.com/Hhz0823/1s-ui/config"
	"github.com/Hhz0823/1s-ui/database"
	"github.com/Hhz0823/1s-ui/database/model"
	"github.com/Hhz0823/1s-ui/logger"

	"github.com/sagernet/sing-box/common/tls"
	"github.com/shirou/gopsutil/v4/cpu"
	"github.com/shirou/gopsutil/v4/disk"
	"github.com/shirou/gopsutil/v4/host"
	"github.com/shirou/gopsutil/v4/mem"
	"github.com/shirou/gopsutil/v4/net"
	"golang.zx2c4.com/wireguard/wgctrl/wgtypes"
)

type ServerService struct{}

func (s *ServerService) GetStatus(request string) *map[string]interface{} {
	status := make(map[string]interface{}, 0)
	requests := strings.Split(request, ",")
	for _, req := range requests {
		switch req {
		case "cpu":
			status["cpu"] = s.GetCpuPercent()
		case "mem":
			status["mem"] = s.GetMemInfo()
		case "dsk":
			status["dsk"] = s.GetDiskInfo()
		case "dio":
			status["dio"] = s.GetDiskIO()
		case "swp":
			status["swp"] = s.GetSwapInfo()
		case "net":
			status["net"] = s.GetNetInfo()
		case "sys":
			status["sys"] = s.GetSystemInfo()
		case "sbd":
			status["sbd"] = s.GetSingboxInfo()
		case "xry":
			status["xry"] = s.GetXrayInfo()
		case "db":
			status["db"] = s.GetDatabaseInfo()
		}
	}
	return &status
}

func (s *ServerService) GetCpuPercent() float64 {
	percents, err := cpu.Percent(0, false)
	if err != nil {
		logger.Warning("get cpu percent failed:", err)
		return 0
	} else {
		return percents[0]
	}
}

func (s *ServerService) GetMemInfo() map[string]interface{} {
	info := make(map[string]interface{}, 0)
	memInfo, err := mem.VirtualMemory()
	if err != nil {
		logger.Warning("get virtual memory failed:", err)
	} else {
		info["current"] = memInfo.Used
		info["total"] = memInfo.Total
	}
	return info
}

func (s *ServerService) GetDiskInfo() map[string]interface{} {
	info := make(map[string]interface{}, 0)
	diskInfo, err := disk.Usage("/")
	if err != nil {
		logger.Warning("get disk usage failed:", err)
	} else {
		info["current"] = diskInfo.Used
		info["total"] = diskInfo.Total
	}
	return info
}

func (s *ServerService) GetDiskIO() map[string]interface{} {
	info := make(map[string]interface{}, 0)
	ioStats, err := disk.IOCounters()
	if err != nil {
		logger.Warning("get disk io counters failed:", err)
	} else if len(ioStats) > 0 {
		infoR, infoW := uint64(0), uint64(0)
		for _, ioStat := range ioStats {
			infoR += ioStat.ReadBytes
			infoW += ioStat.WriteBytes
		}
		info["read"] = infoR
		info["write"] = infoW
	} else {
		logger.Warning("can not find disk io counters")
	}
	return info
}

func (s *ServerService) GetSwapInfo() map[string]interface{} {
	info := make(map[string]interface{}, 0)
	swapInfo, err := mem.SwapMemory()
	if err != nil {
		logger.Warning("get swap memory failed:", err)
	} else {
		info["current"] = swapInfo.Used
		info["total"] = swapInfo.Total
	}
	return info
}

func (s *ServerService) GetNetInfo() map[string]interface{} {
	info := make(map[string]interface{}, 0)
	ioStats, err := net.IOCounters(false)
	if err != nil {
		logger.Warning("get io counters failed:", err)
	} else if len(ioStats) > 0 {
		ioStat := ioStats[0]
		info["sent"] = ioStat.BytesSent
		info["recv"] = ioStat.BytesRecv
		info["psent"] = ioStat.PacketsSent
		info["precv"] = ioStat.PacketsRecv
	} else {
		logger.Warning("can not find io counters")
	}
	return info
}

func (s *ServerService) GetSingboxInfo() map[string]interface{} {
	var rtm runtime.MemStats
	runtime.ReadMemStats(&rtm)
	isRunning := corePtr.IsRunning()
	uptime := uint32(0)
	if isRunning {
		uptime = corePtr.GetInstance().Uptime()
	}
	return map[string]interface{}{
		"running": isRunning,
		"stats": map[string]interface{}{
			"NumGoroutine": uint32(runtime.NumGoroutine()),
			"Alloc":        rtm.Alloc,
			"Uptime":       uptime,
		},
	}
}

func (s *ServerService) GetXrayInfo() map[string]interface{} {
	if config.IsXrayDisabled() {
		return map[string]interface{}{
			"running":    false,
			"disabled":   true,
			"last_error": "",
			"notice":     "Xray-core is disabled on this low-resource server; use sing-box",
			"stats": map[string]interface{}{
				"Uptime": uint32(0),
			},
		}
	}
	if xrayPtr == nil {
		return map[string]interface{}{
			"running":    false,
			"last_error": "",
			"stats": map[string]interface{}{
				"Uptime": uint32(0),
			},
		}
	}
	status := xrayPtr.Status()
	hasXray, err := (&ConfigService{}).HasXrayInbounds()
	if err == nil {
		status["has_inbounds"] = hasXray
		if !hasXray && status["running"] == false {
			status["notice"] = "no Xray-core inbound configured"
		}
	}
	return status
}

// Cluster control-plane (multi-server Agent hub) recommended minimum.
// Normal proxy panel has no hard host requirement.
const (
	MinClusterCPUCores       = 2
	MinClusterMemBytes       = 2 * 1024 * 1024 * 1024 // 2 GiB
	hostRequirementsCacheTTL = 15 * time.Second
)

var hostRequirementsCache = struct {
	sync.Mutex
	loadedAt time.Time
	value    map[string]interface{}
}{}

func currentClusterCapacity() (int, uint64) {
	cpuCount := runtime.NumCPU()
	var memTotal uint64
	if memInfo, err := mem.VirtualMemory(); err == nil {
		memTotal = memInfo.Total
	}
	return cpuCount, memTotal
}

func meetsClusterRequirements(cpuCount int, memTotal uint64) bool {
	return cpuCount >= MinClusterCPUCores && memTotal >= MinClusterMemBytes
}

func buildHostRequirements(cpuCount int, memTotal uint64, agentCount int) map[string]interface{} {
	okCPU := cpuCount >= MinClusterCPUCores
	okMem := memTotal >= MinClusterMemBytes
	meetsCluster := meetsClusterRequirements(cpuCount, memTotal)
	applies := agentCount > 0
	ok := !applies || meetsCluster
	return map[string]interface{}{
		"mode":              map[bool]string{true: "cluster", false: "panel"}[applies],
		"applies":           applies,
		"agent_count":       agentCount,
		"min_cpu_cores":     MinClusterCPUCores,
		"min_mem_bytes":     uint64(MinClusterMemBytes),
		"min_mem_gb":        2,
		"cpu_cores":         cpuCount,
		"mem_total_bytes":   memTotal,
		"ok_cpu":            okCPU,
		"ok_mem":            okMem,
		"ok":                ok,
		"can_enable_agents": meetsCluster,
		"meets_cluster_rec": meetsCluster,
	}
}

func cloneHostRequirements(value map[string]interface{}) map[string]interface{} {
	result := make(map[string]interface{}, len(value))
	for key, item := range value {
		result[key] = item
	}
	return result
}

func invalidateHostRequirementsCache() {
	hostRequirementsCache.Lock()
	hostRequirementsCache.loadedAt = time.Time{}
	hostRequirementsCache.value = nil
	hostRequirementsCache.Unlock()
}

// GetHostRequirements reports host capacity vs the cluster-server minimum.
// applies=true only when this panel is used as a cluster control plane (has Agent nodes).
// Normal panel installs remain allowed, but creating the first Agent requires 2c2G.
func (s *ServerService) GetHostRequirements() map[string]interface{} {
	hostRequirementsCache.Lock()
	defer hostRequirementsCache.Unlock()
	if hostRequirementsCache.value != nil && time.Since(hostRequirementsCache.loadedAt) < hostRequirementsCacheTTL {
		return cloneHostRequirements(hostRequirementsCache.value)
	}

	cpuCount, memTotal := currentClusterCapacity()
	agentCount := 0
	if db := database.GetDB(); db != nil {
		var n int64
		if err := db.Model(&model.AgentNode{}).Count(&n).Error; err == nil {
			agentCount = int(n)
		}
	}
	value := buildHostRequirements(cpuCount, memTotal, agentCount)
	hostRequirementsCache.value = value
	hostRequirementsCache.loadedAt = time.Now()
	return cloneHostRequirements(value)
}

func (s *ServerService) GetSystemInfo() map[string]interface{} {
	info := make(map[string]interface{}, 0)
	var rtm runtime.MemStats
	runtime.ReadMemStats(&rtm)

	info["appMem"] = rtm.Sys
	info["appThreads"] = uint32(runtime.NumGoroutine())
	cpuInfo, err := cpu.Info()
	if err == nil {
		info["cpuType"] = cpuInfo[0].ModelName
	}
	info["cpuCount"] = runtime.NumCPU()
	if memInfo, err := mem.VirtualMemory(); err == nil {
		info["memTotal"] = memInfo.Total
		info["memUsed"] = memInfo.Used
	}
	info["hostName"], _ = os.Hostname()
	info["appVersion"] = config.GetVersion()
	info["requirements"] = s.GetHostRequirements()
	ipv4 := make([]string, 0)
	ipv6 := make([]string, 0)
	// get ip address
	netInterfaces, _ := net.Interfaces()
	for i := 0; i < len(netInterfaces); i++ {
		if len(netInterfaces[i].Flags) > 2 && netInterfaces[i].Flags[0] == "up" && netInterfaces[i].Flags[1] != "loopback" {
			addrs := netInterfaces[i].Addrs

			for _, address := range addrs {
				if strings.Contains(address.Addr, ".") {
					ipv4 = append(ipv4, address.Addr)
				} else if address.Addr[0:6] != "fe80::" {
					ipv6 = append(ipv6, address.Addr)
				}
			}
		}
	}
	info["ipv4"] = ipv4
	info["ipv6"] = ipv6
	info["bootTime"], _ = host.BootTime()

	return info
}

func (s *ServerService) GetLogs(count string, level string) []string {
	c, err := strconv.Atoi(count)
	if err != nil {
		c = 10
	}
	return logger.GetLogs(c, level)
}

func (s *ServerService) GenKeypair(keyType string, options string) []string {
	if len(keyType) == 0 {
		return []string{"No keypair to generate"}
	}

	switch keyType {
	case "ech":
		return s.generateECHKeyPair(options)
	case "tls":
		return s.generateTLSKeyPair(options)
	case "reality":
		return s.generateRealityKeyPair()
	case "wireguard":
		return s.generateWireGuardKey(options)
	}

	return []string{"Failed to generate keypair"}
}

func (s *ServerService) generateECHKeyPair(serverName string) []string {
	configPem, keyPem, err := tls.ECHKeygenDefault(serverName)
	if err != nil {
		return []string{"Failed to generate ECH keypair: ", err.Error()}
	}
	return append(strings.Split(configPem, "\n"), strings.Split(keyPem, "\n")...)
}

func (s *ServerService) generateTLSKeyPair(serverName string) []string {
	privateKeyPem, publicKeyPem, err := tls.GenerateCertificate(nil, nil, time.Now, serverName, time.Now().AddDate(0, 12, 0))
	if err != nil {
		return []string{"Failed to generate TLS keypair: ", err.Error()}
	}
	return append(strings.Split(string(privateKeyPem), "\n"), strings.Split(string(publicKeyPem), "\n")...)
}

func (s *ServerService) generateRealityKeyPair() []string {
	privateKey, err := wgtypes.GeneratePrivateKey()
	if err != nil {
		return []string{"Failed to generate Reality keypair: ", err.Error()}
	}
	publicKey := privateKey.PublicKey()
	return []string{"PrivateKey: " + base64.RawURLEncoding.EncodeToString(privateKey[:]), "PublicKey: " + base64.RawURLEncoding.EncodeToString(publicKey[:])}
}

func (s *ServerService) generateWireGuardKey(pk string) []string {
	if len(pk) > 0 {
		key, _ := wgtypes.ParseKey(pk)
		return []string{key.PublicKey().String()}
	}
	wgKeys, err := wgtypes.GeneratePrivateKey()
	if err != nil {
		return []string{"Failed to generate wireguard keypair: ", err.Error()}
	}
	return []string{"PrivateKey: " + wgKeys.String(), "PublicKey: " + wgKeys.PublicKey().String()}
}

func (s *ServerService) GetDatabaseInfo() map[string]int64 {
	info := make(map[string]int64, 0)
	db := database.GetDB()
	if db == nil {
		return nil
	}

	var clientsCount, inboundsCount, outboundsCount, servicesCount, endpointsCount, clientUp, clientDown int64

	db.Model(&model.Client{}).Count(&clientsCount)
	db.Model(&model.Inbound{}).Count(&inboundsCount)
	db.Model(&model.Outbound{}).Count(&outboundsCount)
	db.Model(&model.Service{}).Count(&servicesCount)
	db.Model(&model.Endpoint{}).Count(&endpointsCount)
	db.Model(&model.Client{}).Select("COALESCE(SUM(up+total_up),0)").Scan(&clientUp)
	db.Model(&model.Client{}).Select("COALESCE(SUM(down+total_down),0)").Scan(&clientDown)

	info["clients"] = clientsCount
	info["inbounds"] = inboundsCount
	info["outbounds"] = outboundsCount
	info["services"] = servicesCount
	info["endpoints"] = endpointsCount
	info["clientUp"] = clientUp
	info["clientDown"] = clientDown

	return info
}
