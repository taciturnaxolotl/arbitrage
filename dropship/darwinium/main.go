package main

import (
	"bytes"
	"crypto/sha256"
	"encoding/json"
	"fmt"
	"log"
	"net/http"
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
	"strings"
	"time"

	"github.com/adrg/xdg"
	"github.com/shirou/gopsutil/v3/cpu"
	"github.com/shirou/gopsutil/v3/disk"
	"github.com/shirou/gopsutil/v3/host"
	"github.com/shirou/gopsutil/v3/load"
	"github.com/shirou/gopsutil/v3/mem"
	"github.com/shirou/gopsutil/v3/process"
)

type Config struct {
	ServerURL string
	ClientID  string
	Token     string
	Hostname  string
}

type RegisterRequest struct {
	Hostname string `json:"hostname"`
	OS       string `json:"os"`
	Arch     string `json:"arch"`
	IP       string `json:"ip"`
	Version  string `json:"version"`
}

type RegisterResponse struct {
	ID    string `json:"id"`
	Token string `json:"token"`
}

type SystemStats struct {
	CPUPercent    float64 `json:"cpu_percent"`
	MemoryPercent float64 `json:"memory_percent"`
	MemoryTotal   uint64  `json:"memory_total"`
	MemoryUsed    uint64  `json:"memory_used"`
	DiskTotal     uint64  `json:"disk_total"`
	DiskUsed      uint64  `json:"disk_used"`
	DiskPercent   float64 `json:"disk_percent"`
	Uptime        uint64  `json:"uptime_seconds"`
	LoadAvg1      float64 `json:"load_avg_1"`
	LoadAvg5      float64 `json:"load_avg_5"`
	LoadAvg15     float64 `json:"load_avg_15"`
}

type OSInfo struct {
	Name         string `json:"name"`
	Version      string `json:"version"`
	Kernel       string `json:"kernel"`
	Platform     string `json:"platform"`
	Hostname     string `json:"hostname"`
	MachineID    string `json:"machine_id"`
	SerialNumber string `json:"serial_number"`
}

type Application struct {
	Name        string `json:"name"`
	Version     string `json:"version"`
	InstallDate string `json:"install_date,omitempty"`
	Publisher   string `json:"publisher,omitempty"`
}

type ProcessInfo struct {
	PID     int32   `json:"pid"`
	Name    string  `json:"name"`
	Status  string  `json:"status"`
	CPU     float64 `json:"cpu_percent"`
	Memory  float64 `json:"memory_percent"`
	Command string  `json:"command,omitempty"`
}

type HeartbeatRequest struct {
	DataHash    string       `json:"data_hash,omitempty"`
	SystemStats *SystemStats `json:"system_stats,omitempty"`
	OSInfo      *OSInfo      `json:"os_info,omitempty"`
}

type HeartbeatResponse struct {
	Commands []Command `json:"commands,omitempty"`
	FullSync bool      `json:"full_sync,omitempty"`
}

type SyncRequest struct {
	SystemStats  *SystemStats  `json:"system_stats,omitempty"`
	OSInfo       *OSInfo       `json:"os_info,omitempty"`
	Applications []Application `json:"applications,omitempty"`
	Processes    []ProcessInfo `json:"processes,omitempty"`
	DataHash     string        `json:"data_hash,omitempty"`
}

type Command struct {
	ID       string         `json:"id"`
	ClientID string         `json:"client_id"`
	Type     string         `json:"type"`
	Payload  map[string]any `json:"payload,omitempty"`
	Status   string         `json:"status"`
}

type CommandAck struct {
	CommandID string `json:"command_id"`
}

type CommandResult struct {
	CommandID string `json:"command_id"`
	Result    any    `json:"result"`
	Error     string `json:"error,omitempty"`
}

var lastDataHash string
var statePath string

type SavedState struct {
	ClientID  string `json:"client_id"`
	Token     string `json:"token"`
	ServerURL string `json:"server_url"`
}

func stateFilePath() string {
	return filepath.Join(xdg.StateHome, "darwinium", "client.json")
}

func saveState(cfg *Config) error {
	path := stateFilePath()
	os.MkdirAll(filepath.Dir(path), 0700)
	state := SavedState{
		ClientID:  cfg.ClientID,
		Token:     cfg.Token,
		ServerURL: cfg.ServerURL,
	}
	data, err := json.MarshalIndent(state, "", "  ")
	if err != nil {
		return err
	}
	return os.WriteFile(path, data, 0600)
}

func loadState() (*SavedState, error) {
	path := stateFilePath()
	data, err := os.ReadFile(path)
	if err != nil {
		return nil, err
	}
	var state SavedState
	if err := json.Unmarshal(data, &state); err != nil {
		return nil, err
	}
	return &state, nil
}

func main() {
	cfg := Config{
		ServerURL: envOrDefault("CONTROLPLANE_URL", "http://localhost:8080"),
		ClientID:  os.Getenv("CLIENT_ID"),
		Token:     os.Getenv("CLIENT_TOKEN"),
	}

	hostname, _ := os.Hostname()
	cfg.Hostname = hostname

	log.Printf("Darwinium client starting (server=%s)", cfg.ServerURL)

	if cfg.ClientID == "" || cfg.Token == "" {
		if saved, err := loadState(); err == nil && saved.ClientID != "" && saved.Token != "" {
			if cfg.ServerURL == saved.ServerURL || os.Getenv("CONTROLPLANE_URL") == "" {
				cfg.ClientID = saved.ClientID
				cfg.Token = saved.Token
				log.Printf("Loaded saved credentials (id=%s)", cfg.ClientID)
			} else {
				log.Println("Server URL changed, re-registering...")
			}
		}
	}

	if cfg.ClientID == "" || cfg.Token == "" {
		log.Println("No credentials found, registering with control plane...")
		if err := registerWithRetry(&cfg); err != nil {
			log.Fatalf("registration failed: %v", err)
		}
		log.Printf("Registered! ID=%s", cfg.ClientID)
	}

	if err := saveState(&cfg); err != nil {
		log.Printf("Warning: could not save state: %v", err)
	} else {
		log.Printf("State saved to %s", stateFilePath())
	}

	go heartbeatLoop(&cfg)
	dataLoop(&cfg)
}

func envOrDefault(key, def string) string {
	if v := os.Getenv(key); v != "" {
		return v
	}
	return def
}

func registerWithRetry(cfg *Config) error {
	for attempt := 1; ; attempt++ {
		err := register(cfg)
		if err == nil {
			return nil
		}
		backoff := time.Duration(attempt*attempt) * time.Second
		if backoff > 60*time.Second {
			backoff = 60 * time.Second
		}
		log.Printf("Registration attempt %d failed: %v — retrying in %v", attempt, err, backoff)
		time.Sleep(backoff)
	}
}

func register(cfg *Config) error {
	req := RegisterRequest{
		Hostname: cfg.Hostname,
		OS:       runtime.GOOS,
		Arch:     runtime.GOARCH,
		IP:       getLocalIP(),
		Version:  "0.2.0",
	}

	body, err := json.Marshal(req)
	if err != nil {
		return err
	}

	resp, err := http.Post(cfg.ServerURL+"/api/register", "application/json", bytes.NewReader(body))
	if err != nil {
		return err
	}
	defer resp.Body.Close()

	if resp.StatusCode != http.StatusOK && resp.StatusCode != http.StatusCreated {
		return fmt.Errorf("registration returned %d", resp.StatusCode)
	}

	var reg RegisterResponse
	if err := json.NewDecoder(resp.Body).Decode(&reg); err != nil {
		return err
	}

	cfg.ClientID = reg.ID
	cfg.Token = reg.Token
	return nil
}

func heartbeatLoop(cfg *Config) {
	ticker := time.NewTicker(15 * time.Second)
	for range ticker.C {
		stats, _ := collectSystemStats()
		osInfo, _ := collectOSInfo()

		dataHash := computeDataHash(stats, osInfo)

		hb := HeartbeatRequest{
			DataHash:    dataHash,
			SystemStats: stats,
			OSInfo:      osInfo,
		}

		var resp HeartbeatResponse
		code, err := postAuth(cfg, "/api/heartbeat", hb, &resp)
		if err != nil {
			if code == http.StatusUnauthorized {
				log.Println("Credentials rejected, re-registering...")
				reRegister(cfg)
				continue
			}
			log.Printf("heartbeat error: %v", err)
			continue
		}

		if resp.FullSync {
			log.Println("Server requests full sync, triggering data push...")
			go pushFullSync(cfg)
		}

		for _, cmd := range resp.Commands {
			go executeAndReport(cfg, cmd)
		}
	}
}

func dataLoop(cfg *Config) {
	ticker := time.NewTicker(120 * time.Second)
	for range ticker.C {
		pushFullSync(cfg)
	}
}

func pushFullSync(cfg *Config) {
	stats, _ := collectSystemStats()
	osInfo, _ := collectOSInfo()
	apps, _ := collectApplications()
	procs, _ := collectProcesses()

	dataHash := computeDataHash(stats, osInfo)

	sync := SyncRequest{
		SystemStats:  stats,
		OSInfo:       osInfo,
		Applications: apps,
		Processes:    procs,
		DataHash:     dataHash,
	}

	if _, err := postAuth(cfg, "/api/sync", sync, nil); err != nil {
		log.Printf("sync error: %v", err)
	} else {
		lastDataHash = dataHash
	}
}

func reRegister(cfg *Config) {
	cfg.ClientID = ""
	cfg.Token = ""
	if err := registerWithRetry(cfg); err != nil {
		log.Printf("re-registration failed: %v", err)
		return
	}
	log.Printf("Re-registered! ID=%s", cfg.ClientID)
	saveState(cfg)
}

func executeAndReport(cfg *Config, cmd Command) {
	log.Printf("Executing command: %s (type=%s)", cmd.ID, cmd.Type)

	if _, err := postAuth(cfg, "/api/commands/ack", CommandAck{CommandID: cmd.ID}, nil); err != nil {
		log.Printf("ack error for %s: %v", cmd.ID, err)
	}

	if cmd.Type == "sync" {
		pushFullSync(cfg)
		result := CommandResult{CommandID: cmd.ID, Result: "sync triggered"}
		if _, err := postAuth(cfg, "/api/commands/result", result, nil); err != nil {
			log.Printf("result report error: %v", err)
		}
		log.Printf("Command %s completed", cmd.ID)
		return
	}

	result := executeCommand(cmd)

	if _, err := postAuth(cfg, "/api/commands/result", result, nil); err != nil {
		log.Printf("result report error: %v", err)
	}
	log.Printf("Command %s completed", cmd.ID)
}

func executeCommand(cmd Command) CommandResult {
	result := CommandResult{CommandID: cmd.ID}

	switch cmd.Type {
	case "exec":
		cmdStr, _ := cmd.Payload["command"].(string)
		if cmdStr == "" {
			result.Error = "missing command payload"
			return result
		}
		out, err := exec.Command("sh", "-c", cmdStr).CombinedOutput()
		if err != nil {
			result.Error = err.Error()
		}
		result.Result = string(out)

	case "list_apps":
		apps, err := collectApplications()
		if err != nil {
			result.Error = err.Error()
		}
		result.Result = apps

	case "list_processes":
		procs, err := collectProcesses()
		if err != nil {
			result.Error = err.Error()
		}
		result.Result = procs

	case "get_stats":
		stats, err := collectSystemStats()
		if err != nil {
			result.Error = err.Error()
		}
		result.Result = stats

	case "get_os_info":
		info, err := collectOSInfo()
		if err != nil {
			result.Error = err.Error()
		}
		result.Result = info

	default:
		result.Error = fmt.Sprintf("unknown command type: %s", cmd.Type)
	}

	return result
}

func computeDataHash(stats *SystemStats, osInfo *OSInfo) string {
	h := sha256.New()
	if stats != nil {
		fmt.Fprintf(h, "stats:%.2f:%.2f:%d:%d:%d:%d:%.2f:%d", stats.CPUPercent, stats.MemoryPercent, stats.MemoryTotal, stats.MemoryUsed, stats.DiskTotal, stats.DiskUsed, stats.DiskPercent, stats.Uptime)
	}
	if osInfo != nil {
		fmt.Fprintf(h, "os:%s:%s:%s", osInfo.Name, osInfo.Version, osInfo.Kernel)
	}
	return fmt.Sprintf("%x", h.Sum(nil))[:16]
}

func collectSystemStats() (*SystemStats, error) {
	cpuPcts, _ := cpu.Percent(0, false)
	cpuPct := 0.0
	if len(cpuPcts) > 0 {
		cpuPct = cpuPcts[0]
	}

	vmStat, _ := mem.VirtualMemory()
	diskStat, _ := disk.Usage("/")
	hostInfo, _ := host.Info()
	loadAvg, _ := load.Avg()

	stats := &SystemStats{}

	if vmStat != nil {
		stats.MemoryPercent = vmStat.UsedPercent
		stats.MemoryTotal = vmStat.Total
		stats.MemoryUsed = vmStat.Used
	}
	if diskStat != nil {
		stats.DiskTotal = diskStat.Total
		stats.DiskUsed = diskStat.Used
		stats.DiskPercent = diskStat.UsedPercent
	}
	if hostInfo != nil {
		stats.Uptime = hostInfo.Uptime
	}
	stats.CPUPercent = cpuPct
	if loadAvg != nil {
		stats.LoadAvg1 = loadAvg.Load1
		stats.LoadAvg5 = loadAvg.Load5
		stats.LoadAvg15 = loadAvg.Load15
	}

	return stats, nil
}

func collectOSInfo() (*OSInfo, error) {
	hostInfo, _ := host.Info()
	info := &OSInfo{}

	if hostInfo != nil {
		info.Name = hostInfo.OS
		info.Version = hostInfo.PlatformVersion
		info.Kernel = hostInfo.KernelVersion
		info.Platform = hostInfo.Platform
		info.Hostname = hostInfo.Hostname
		info.MachineID = hostInfo.HostID
	}

	if runtime.GOOS == "darwin" {
		out, err := exec.Command("system_profiler", "SPHardwareDataType").Output()
		if err == nil {
			for _, line := range strings.Split(string(out), "\n") {
				if strings.Contains(line, "Serial Number") {
					parts := strings.SplitN(line, ":", 2)
					if len(parts) == 2 {
						info.SerialNumber = strings.TrimSpace(parts[1])
					}
				}
			}
		}
	}

	return info, nil
}

func collectApplications() ([]Application, error) {
	if runtime.GOOS != "darwin" {
		return nil, nil
	}

	out, err := exec.Command("system_profiler", "SPApplicationsDataType", "-json").Output()
	if err != nil {
		return nil, err
	}

	var data map[string][]struct {
		Name         string `json:"_name"`
		Version      string `json:"version,omitempty"`
		ObtainedFrom string `json:"obtained_from,omitempty"`
		InstallDate  string `json:"install_date,omitempty"`
	}
	if err := json.Unmarshal(out, &data); err != nil {
		return nil, err
	}

	var apps []Application
	if spApps, ok := data["SPApplicationsDataType"]; ok {
		for _, a := range spApps {
			apps = append(apps, Application{
				Name:        a.Name,
				Version:     a.Version,
				InstallDate: a.InstallDate,
				Publisher:   a.ObtainedFrom,
			})
		}
	}

	return apps, nil
}

func collectProcesses() ([]ProcessInfo, error) {
	pids, err := process.Processes()
	if err != nil {
		return nil, err
	}

	var procs []ProcessInfo
	for _, p := range pids {
		name, _ := p.Name()
		cpuPct, _ := p.CPUPercent()
		memPct, _ := p.MemoryPercent()
		status, _ := p.Status()
		cmdline, _ := p.Cmdline()

		var statusStr string
		if len(status) > 0 {
			statusStr = status[0]
		}

		procs = append(procs, ProcessInfo{
			PID:     p.Pid,
			Name:    name,
			Status:  statusStr,
			CPU:     cpuPct,
			Memory:  float64(memPct),
			Command: cmdline,
		})
	}

	return procs, nil
}

func getLocalIP() string {
	out, err := exec.Command("ipconfig", "getifaddr", "en0").Output()
	if err != nil {
		return "unknown"
	}
	return strings.TrimSpace(string(out))
}

func postAuth(cfg *Config, path string, payload interface{}, response interface{}) (int, error) {
	body, err := json.Marshal(payload)
	if err != nil {
		return 0, err
	}

	req, err := http.NewRequest("POST", cfg.ServerURL+path, bytes.NewReader(body))
	if err != nil {
		return 0, err
	}
	req.SetBasicAuth(cfg.ClientID, cfg.Token)
	req.Header.Set("Content-Type", "application/json")

	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		return 0, err
	}
	defer resp.Body.Close()

	if resp.StatusCode >= 400 {
		return resp.StatusCode, fmt.Errorf("server returned %d", resp.StatusCode)
	}

	if response != nil {
		json.NewDecoder(resp.Body).Decode(response)
	}

	return resp.StatusCode, nil
}
