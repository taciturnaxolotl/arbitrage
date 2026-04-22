package models

import "time"

type Client struct {
	ID            string    `json:"id"`
	Token         string    `json:"token,omitempty"`
	Hostname      string    `json:"hostname"`
	OS            string    `json:"os"`
	Arch          string    `json:"arch"`
	IP            string    `json:"ip"`
	Version       string    `json:"version"`
	Status        string    `json:"status"`
	LastHeartbeat time.Time `json:"last_heartbeat"`
	RegisteredAt  time.Time `json:"registered_at"`
	SystemStats   *SystemStats   `json:"system_stats,omitempty"`
	OSInfo        *OSInfo        `json:"os_info,omitempty"`
	Applications  []Application  `json:"applications,omitempty"`
	Processes     []Process      `json:"processes,omitempty"`
	DataHash      string         `json:"data_hash,omitempty"`
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
	Name         string   `json:"name"`
	Version      string   `json:"version"`
	InstallDate  string   `json:"install_date,omitempty"`
	Publisher    string   `json:"publisher,omitempty"`
	Path         string   `json:"path,omitempty"`
	ArchKind     string   `json:"arch_kind,omitempty"`
	LastModified string   `json:"last_modified,omitempty"`
	SignedBy     []string `json:"signed_by,omitempty"`
}

type Process struct {
	PID         int32   `json:"pid"`
	Name        string  `json:"name"`
	Status      string  `json:"status"`
	CPU         float64 `json:"cpu_percent"`
	Memory      float64 `json:"memory_percent"`
	Command     string  `json:"command,omitempty"`
	Exe         string  `json:"exe,omitempty"`
	Cwd         string  `json:"cwd,omitempty"`
	Username    string  `json:"username,omitempty"`
	Ppid        int32   `json:"ppid"`
	CreateTime  int64   `json:"create_time,omitempty"`
	NumThreads  int32   `json:"num_threads"`
	NumFDs      int32   `json:"num_fds"`
	RSS         uint64  `json:"rss,omitempty"`
	VMS         uint64  `json:"vms,omitempty"`
	ReadBytes   uint64  `json:"read_bytes,omitempty"`
	WriteBytes  uint64  `json:"write_bytes,omitempty"`
}

type Command struct {
	ID          string         `json:"id"`
	ClientID    string         `json:"client_id"`
	Type        string         `json:"type"`
	Payload     map[string]any `json:"payload,omitempty"`
	Status      string         `json:"status"`
	Result      any            `json:"result,omitempty"`
	Error       string         `json:"error,omitempty"`
	CreatedAt   time.Time      `json:"created_at"`
	AcknowledgedAt *time.Time   `json:"acknowledged_at,omitempty"`
	CompletedAt   *time.Time   `json:"completed_at,omitempty"`
	Timeout     time.Duration  `json:"timeout,omitempty"`
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
	Processes    []Process     `json:"processes,omitempty"`
	DataHash     string        `json:"data_hash,omitempty"`
}

type CommandRequest struct {
	Type    string         `json:"type"`
	Payload map[string]any `json:"payload,omitempty"`
	Timeout int            `json:"timeout,omitempty"`
}

type CommandAck struct {
	CommandID string `json:"command_id"`
}

type CommandResult struct {
	CommandID string `json:"command_id"`
	Result    any    `json:"result"`
	Error     string `json:"error,omitempty"`
}

type DashboardStats struct {
	TotalClients       int            `json:"total_clients"`
	OnlineClients      int            `json:"online_clients"`
	OfflineClients     int            `json:"offline_clients"`
	OSDistribution     map[string]int `json:"os_distribution"`
	ArchDistribution   map[string]int `json:"arch_distribution"`
	StatusDistribution map[string]int `json:"status_distribution"`
	AvgCPU             float64        `json:"avg_cpu"`
	AvgMemory          float64        `json:"avg_memory"`
	AvgDisk            float64        `json:"avg_disk"`
	Clients            []ClientSummary `json:"clients"`
}

type ClientSummary struct {
	ID            string    `json:"id"`
	Hostname      string    `json:"hostname"`
	OS            string    `json:"os"`
	Arch          string    `json:"arch"`
	Status        string    `json:"status"`
	LastHeartbeat time.Time `json:"last_heartbeat"`
	CPUPercent    float64   `json:"cpu_percent"`
	MemoryPercent float64   `json:"memory_percent"`
}
