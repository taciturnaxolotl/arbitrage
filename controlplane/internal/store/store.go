package store

import (
	"crypto/rand"
	"database/sql"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"log"
	"sync"
	"time"

	_ "modernc.org/sqlite"

	"tangled.sh/dunkirk.sh/arbitrage/controlplane/internal/models"
)

const offlineThreshold = 30 * time.Second
const defaultCommandTimeout = 5 * time.Minute

type Store struct {
	db          *sql.DB
	cmdWaiters  map[string][]chan struct{}
	cmdWaitersMu sync.Mutex
}

func New(dbPath string) *Store {
	if dbPath == "" {
		dbPath = "controlplane.db"
	}

	db, err := sql.Open("sqlite", dbPath)
	if err != nil {
		log.Fatalf("failed to open sqlite: %v", err)
	}

	db.Exec("PRAGMA journal_mode=WAL")
	db.Exec("PRAGMA foreign_keys=ON")

	s := &Store{db: db, cmdWaiters: make(map[string][]chan struct{})}
	s.migrate()
	return s
}

func (s *Store) migrate() {
	stmts := []string{
		`CREATE TABLE IF NOT EXISTS clients (
			id TEXT PRIMARY KEY,
			token TEXT NOT NULL UNIQUE,
			hostname TEXT NOT NULL,
			os TEXT NOT NULL,
			arch TEXT NOT NULL,
			internal_ip TEXT NOT NULL DEFAULT '',
			external_ip TEXT NOT NULL DEFAULT '',
			version TEXT NOT NULL,
			status TEXT NOT NULL DEFAULT 'online',
			data_hash TEXT NOT NULL DEFAULT '',
			last_heartbeat DATETIME NOT NULL,
			registered_at DATETIME NOT NULL
		)`,
		`CREATE INDEX IF NOT EXISTS idx_clients_token ON clients(token)`,
		`CREATE TABLE IF NOT EXISTS system_stats (
			client_id TEXT PRIMARY KEY REFERENCES clients(id) ON DELETE CASCADE,
			cpu_percent REAL NOT NULL DEFAULT 0,
			memory_percent REAL NOT NULL DEFAULT 0,
			memory_total INTEGER NOT NULL DEFAULT 0,
			memory_used INTEGER NOT NULL DEFAULT 0,
			disk_total INTEGER NOT NULL DEFAULT 0,
			disk_used INTEGER NOT NULL DEFAULT 0,
			disk_percent REAL NOT NULL DEFAULT 0,
			uptime_seconds INTEGER NOT NULL DEFAULT 0,
			load_avg_1 REAL NOT NULL DEFAULT 0,
			load_avg_5 REAL NOT NULL DEFAULT 0,
			load_avg_15 REAL NOT NULL DEFAULT 0
		)`,
		`CREATE TABLE IF NOT EXISTS os_info (
			client_id TEXT PRIMARY KEY REFERENCES clients(id) ON DELETE CASCADE,
			name TEXT NOT NULL DEFAULT '',
			version TEXT NOT NULL DEFAULT '',
			kernel TEXT NOT NULL DEFAULT '',
			platform TEXT NOT NULL DEFAULT '',
			hostname TEXT NOT NULL DEFAULT '',
			machine_id TEXT NOT NULL DEFAULT '',
			serial_number TEXT NOT NULL DEFAULT ''
		)`,
		`CREATE TABLE IF NOT EXISTS applications (
			id INTEGER PRIMARY KEY AUTOINCREMENT,
			client_id TEXT NOT NULL REFERENCES clients(id) ON DELETE CASCADE,
			name TEXT NOT NULL,
			version TEXT NOT NULL DEFAULT '',
			install_date TEXT NOT NULL DEFAULT '',
			publisher TEXT NOT NULL DEFAULT '',
			path TEXT NOT NULL DEFAULT '',
			arch_kind TEXT NOT NULL DEFAULT '',
			last_modified TEXT NOT NULL DEFAULT '',
			signed_by TEXT NOT NULL DEFAULT ''
		)`,
		`CREATE INDEX IF NOT EXISTS idx_applications_client_id ON applications(client_id)`,
		`CREATE TABLE IF NOT EXISTS processes (
			id INTEGER PRIMARY KEY AUTOINCREMENT,
			client_id TEXT NOT NULL REFERENCES clients(id) ON DELETE CASCADE,
			pid INTEGER NOT NULL,
			name TEXT NOT NULL,
			status TEXT NOT NULL DEFAULT '',
			cpu_percent REAL NOT NULL DEFAULT 0,
			memory_percent REAL NOT NULL DEFAULT 0,
			command TEXT NOT NULL DEFAULT '',
			exe TEXT NOT NULL DEFAULT '',
			cwd TEXT NOT NULL DEFAULT '',
			username TEXT NOT NULL DEFAULT '',
			ppid INTEGER NOT NULL DEFAULT 0,
			create_time INTEGER NOT NULL DEFAULT 0,
			num_threads INTEGER NOT NULL DEFAULT 0,
			num_fds INTEGER NOT NULL DEFAULT 0,
			rss INTEGER NOT NULL DEFAULT 0,
			vms INTEGER NOT NULL DEFAULT 0,
			read_bytes INTEGER NOT NULL DEFAULT 0,
			write_bytes INTEGER NOT NULL DEFAULT 0
		)`,
		`CREATE INDEX IF NOT EXISTS idx_processes_client_id ON processes(client_id)`,
		`CREATE TABLE IF NOT EXISTS commands (
			id TEXT PRIMARY KEY,
			client_id TEXT NOT NULL REFERENCES clients(id) ON DELETE CASCADE,
			type TEXT NOT NULL,
			payload TEXT NOT NULL DEFAULT '{}',
			status TEXT NOT NULL DEFAULT 'pending',
			result TEXT NOT NULL DEFAULT '',
			error TEXT NOT NULL DEFAULT '',
			timeout_seconds INTEGER NOT NULL DEFAULT 300,
			created_at DATETIME NOT NULL,
			acknowledged_at DATETIME,
			completed_at DATETIME
		)`,
		`CREATE INDEX IF NOT EXISTS idx_commands_client_id ON commands(client_id)`,
		`CREATE INDEX IF NOT EXISTS idx_commands_status ON commands(status)`,
		`CREATE TABLE IF NOT EXISTS users (
			id TEXT PRIMARY KEY,
			email TEXT NOT NULL UNIQUE,
			name TEXT NOT NULL DEFAULT '',
			role TEXT NOT NULL DEFAULT 'viewer',
			created_at DATETIME NOT NULL
		)`,
	}
	for _, stmt := range stmts {
		if _, err := s.db.Exec(stmt); err != nil {
			log.Fatalf("migration failed: %v\n%s", err, stmt)
		}
	}

	alterStmts := []string{
		`ALTER TABLE commands ADD COLUMN error TEXT NOT NULL DEFAULT ''`,
		`ALTER TABLE commands ADD COLUMN timeout_seconds INTEGER NOT NULL DEFAULT 300`,
		`ALTER TABLE commands ADD COLUMN acknowledged_at DATETIME`,
		`ALTER TABLE applications ADD COLUMN path TEXT NOT NULL DEFAULT ''`,
		`ALTER TABLE applications ADD COLUMN arch_kind TEXT NOT NULL DEFAULT ''`,
		`ALTER TABLE applications ADD COLUMN last_modified TEXT NOT NULL DEFAULT ''`,
		`ALTER TABLE applications ADD COLUMN signed_by TEXT NOT NULL DEFAULT ''`,
		`ALTER TABLE processes ADD COLUMN exe TEXT NOT NULL DEFAULT ''`,
		`ALTER TABLE processes ADD COLUMN cwd TEXT NOT NULL DEFAULT ''`,
		`ALTER TABLE processes ADD COLUMN username TEXT NOT NULL DEFAULT ''`,
		`ALTER TABLE processes ADD COLUMN ppid INTEGER NOT NULL DEFAULT 0`,
		`ALTER TABLE processes ADD COLUMN create_time INTEGER NOT NULL DEFAULT 0`,
		`ALTER TABLE processes ADD COLUMN num_threads INTEGER NOT NULL DEFAULT 0`,
		`ALTER TABLE processes ADD COLUMN num_fds INTEGER NOT NULL DEFAULT 0`,
		`ALTER TABLE processes ADD COLUMN rss INTEGER NOT NULL DEFAULT 0`,
		`ALTER TABLE processes ADD COLUMN vms INTEGER NOT NULL DEFAULT 0`,
		`ALTER TABLE processes ADD COLUMN read_bytes INTEGER NOT NULL DEFAULT 0`,
		`ALTER TABLE processes ADD COLUMN write_bytes INTEGER NOT NULL DEFAULT 0`,
		`ALTER TABLE clients ADD COLUMN internal_ip TEXT NOT NULL DEFAULT ''`,
		`ALTER TABLE clients ADD COLUMN external_ip TEXT NOT NULL DEFAULT ''`,
	}
	for _, stmt := range alterStmts {
		s.db.Exec(stmt)
	}
}

func (s *Store) RegisterClient(req models.RegisterRequest) *models.RegisterResponse {
	id := generateUUID()
	token := generateToken()
	now := time.Now()

	_, err := s.db.Exec(`INSERT INTO clients (id, token, hostname, os, arch, internal_ip, external_ip, version, status, last_heartbeat, registered_at)
		VALUES (?, ?, ?, ?, ?, ?, ?, ?, 'online', ?, ?)`, id, token, req.Hostname, req.OS, req.Arch, req.InternalIP, req.ExternalIP, req.Version, now, now)
	if err != nil {
		log.Printf("register client db error: %v", err)
		return nil
	}

	return &models.RegisterResponse{ID: id, Token: token}
}

func (s *Store) AuthenticateClient(id, token string) bool {
	var count int
	err := s.db.QueryRow(`SELECT COUNT(*) FROM clients WHERE id=? AND token=?`, id, token).Scan(&count)
	if err != nil {
		return false
	}
	return count == 1
}

func (s *Store) UpdateHeartbeat(clientID string, hb models.HeartbeatRequest) *models.HeartbeatResponse {
	now := time.Now()

	res, err := s.db.Exec(`UPDATE clients SET status='online', last_heartbeat=?, data_hash=?, internal_ip=COALESCE(NULLIF(?,''), internal_ip), external_ip=COALESCE(NULLIF(?,''), external_ip) WHERE id=?`, now, hb.DataHash, hb.InternalIP, hb.ExternalIP, clientID)
	if err != nil {
		return nil
	}
	rows, _ := res.RowsAffected()
	if rows == 0 {
		return nil
	}

	if hb.SystemStats != nil {
		s.db.Exec(`INSERT INTO system_stats (client_id, cpu_percent, memory_percent, memory_total, memory_used, disk_total, disk_used, disk_percent, uptime_seconds, load_avg_1, load_avg_5, load_avg_15)
			VALUES (?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?)
			ON CONFLICT(client_id) DO UPDATE SET cpu_percent=excluded.cpu_percent, memory_percent=excluded.memory_percent, memory_total=excluded.memory_total, memory_used=excluded.memory_used, disk_total=excluded.disk_total, disk_used=excluded.disk_used, disk_percent=excluded.disk_percent, uptime_seconds=excluded.uptime_seconds, load_avg_1=excluded.load_avg_1, load_avg_5=excluded.load_avg_5, load_avg_15=excluded.load_avg_15`,
			clientID, hb.SystemStats.CPUPercent, hb.SystemStats.MemoryPercent, hb.SystemStats.MemoryTotal, hb.SystemStats.MemoryUsed,
			hb.SystemStats.DiskTotal, hb.SystemStats.DiskUsed, hb.SystemStats.DiskPercent, hb.SystemStats.Uptime,
			hb.SystemStats.LoadAvg1, hb.SystemStats.LoadAvg5, hb.SystemStats.LoadAvg15)
	}

	if hb.OSInfo != nil {
		s.db.Exec(`INSERT INTO os_info (client_id, name, version, kernel, platform, hostname, machine_id, serial_number)
			VALUES (?, ?, ?, ?, ?, ?, ?, ?)
			ON CONFLICT(client_id) DO UPDATE SET name=excluded.name, version=excluded.version, kernel=excluded.kernel, platform=excluded.platform, hostname=excluded.hostname, machine_id=excluded.machine_id, serial_number=excluded.serial_number`,
			clientID, hb.OSInfo.Name, hb.OSInfo.Version, hb.OSInfo.Kernel, hb.OSInfo.Platform, hb.OSInfo.Hostname, hb.OSInfo.MachineID, hb.OSInfo.SerialNumber)
	}

	resp := &models.HeartbeatResponse{}

	var storedHash string
	s.db.QueryRow(`SELECT data_hash FROM clients WHERE id=?`, clientID).Scan(&storedHash)
	if storedHash != hb.DataHash && hb.DataHash != "" {
		resp.FullSync = true
	}

	pendingCmds := s.GetPendingCommands(clientID)
	if len(pendingCmds) > 0 {
		resp.Commands = pendingCmds
	}

	return resp
}

func (s *Store) FullSync(clientID string, sync models.SyncRequest) bool {
	tx, err := s.db.Begin()
	if err != nil {
		return false
	}

	if sync.SystemStats != nil {
		tx.Exec(`INSERT INTO system_stats (client_id, cpu_percent, memory_percent, memory_total, memory_used, disk_total, disk_used, disk_percent, uptime_seconds, load_avg_1, load_avg_5, load_avg_15)
			VALUES (?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?)
			ON CONFLICT(client_id) DO UPDATE SET cpu_percent=excluded.cpu_percent, memory_percent=excluded.memory_percent, memory_total=excluded.memory_total, memory_used=excluded.memory_used, disk_total=excluded.disk_total, disk_used=excluded.disk_used, disk_percent=excluded.disk_percent, uptime_seconds=excluded.uptime_seconds, load_avg_1=excluded.load_avg_1, load_avg_5=excluded.load_avg_5, load_avg_15=excluded.load_avg_15`,
			clientID, sync.SystemStats.CPUPercent, sync.SystemStats.MemoryPercent, sync.SystemStats.MemoryTotal, sync.SystemStats.MemoryUsed,
			sync.SystemStats.DiskTotal, sync.SystemStats.DiskUsed, sync.SystemStats.DiskPercent, sync.SystemStats.Uptime,
			sync.SystemStats.LoadAvg1, sync.SystemStats.LoadAvg5, sync.SystemStats.LoadAvg15)
	}

	if sync.OSInfo != nil {
		tx.Exec(`INSERT INTO os_info (client_id, name, version, kernel, platform, hostname, machine_id, serial_number)
			VALUES (?, ?, ?, ?, ?, ?, ?, ?)
			ON CONFLICT(client_id) DO UPDATE SET name=excluded.name, version=excluded.version, kernel=excluded.kernel, platform=excluded.platform, hostname=excluded.hostname, machine_id=excluded.machine_id, serial_number=excluded.serial_number`,
			clientID, sync.OSInfo.Name, sync.OSInfo.Version, sync.OSInfo.Kernel, sync.OSInfo.Platform, sync.OSInfo.Hostname, sync.OSInfo.MachineID, sync.OSInfo.SerialNumber)
	}

	if sync.Applications != nil {
		tx.Exec(`DELETE FROM applications WHERE client_id=?`, clientID)
		for _, a := range sync.Applications {
			signedByJSON, _ := json.Marshal(a.SignedBy)
			tx.Exec(`INSERT INTO applications (client_id, name, version, install_date, publisher, path, arch_kind, last_modified, signed_by) VALUES (?, ?, ?, ?, ?, ?, ?, ?, ?)`,
				clientID, a.Name, a.Version, a.InstallDate, a.Publisher, a.Path, a.ArchKind, a.LastModified, string(signedByJSON))
		}
	}

	if sync.Processes != nil {
		tx.Exec(`DELETE FROM processes WHERE client_id=?`, clientID)
		for _, p := range sync.Processes {
			tx.Exec(`INSERT INTO processes (client_id, pid, name, status, cpu_percent, memory_percent, command, exe, cwd, username, ppid, create_time, num_threads, num_fds, rss, vms, read_bytes, write_bytes) VALUES (?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?)`,
				clientID, p.PID, p.Name, p.Status, p.CPU, p.Memory, p.Command, p.Exe, p.Cwd, p.Username, p.Ppid, p.CreateTime, p.NumThreads, p.NumFDs, p.RSS, p.VMS, p.ReadBytes, p.WriteBytes)
		}
	}

	tx.Exec(`UPDATE clients SET data_hash=?, last_heartbeat=? WHERE id=?`, sync.DataHash, time.Now(), clientID)

	if err := tx.Commit(); err != nil {
		log.Printf("sync commit error: %v", err)
		return false
	}
	return true
}

func (s *Store) GetClient(id string) (*models.Client, bool) {
	client := &models.Client{}
	err := s.db.QueryRow(`SELECT id, hostname, os, arch, internal_ip, external_ip, version, status, data_hash, last_heartbeat, registered_at FROM clients WHERE id=?`, id).
		Scan(&client.ID, &client.Hostname, &client.OS, &client.Arch, &client.InternalIP, &client.ExternalIP, &client.Version, &client.Status, &client.DataHash, &client.LastHeartbeat, &client.RegisteredAt)
	if err != nil {
		return nil, false
	}

	client.SystemStats = s.getSystemStats(id)
	client.OSInfo = s.getOSInfo(id)
	client.Applications = s.getApplications(id)
	client.Processes = s.getProcesses(id)

	return client, true
}

func (s *Store) getSystemStats(clientID string) *models.SystemStats {
	stats := &models.SystemStats{}
	err := s.db.QueryRow(`SELECT cpu_percent, memory_percent, memory_total, memory_used, disk_total, disk_used, disk_percent, uptime_seconds, load_avg_1, load_avg_5, load_avg_15 FROM system_stats WHERE client_id=?`, clientID).
		Scan(&stats.CPUPercent, &stats.MemoryPercent, &stats.MemoryTotal, &stats.MemoryUsed, &stats.DiskTotal, &stats.DiskUsed, &stats.DiskPercent, &stats.Uptime, &stats.LoadAvg1, &stats.LoadAvg5, &stats.LoadAvg15)
	if err != nil {
		return nil
	}
	return stats
}

func (s *Store) getOSInfo(clientID string) *models.OSInfo {
	info := &models.OSInfo{}
	err := s.db.QueryRow(`SELECT name, version, kernel, platform, hostname, machine_id, serial_number FROM os_info WHERE client_id=?`, clientID).
		Scan(&info.Name, &info.Version, &info.Kernel, &info.Platform, &info.Hostname, &info.MachineID, &info.SerialNumber)
	if err != nil {
		return nil
	}
	return info
}

func (s *Store) getApplications(clientID string) []models.Application {
	rows, err := s.db.Query(`SELECT name, version, install_date, publisher, path, arch_kind, last_modified, signed_by FROM applications WHERE client_id=? ORDER BY name`, clientID)
	if err != nil {
		return nil
	}
	defer rows.Close()

	var apps []models.Application
	for rows.Next() {
		var a models.Application
		var signedByJSON string
		rows.Scan(&a.Name, &a.Version, &a.InstallDate, &a.Publisher, &a.Path, &a.ArchKind, &a.LastModified, &signedByJSON)
		if signedByJSON != "" && signedByJSON != "null" {
			json.Unmarshal([]byte(signedByJSON), &a.SignedBy)
		}
		apps = append(apps, a)
	}
	return apps
}

func (s *Store) getProcesses(clientID string) []models.Process {
	rows, err := s.db.Query(`SELECT pid, name, status, cpu_percent, memory_percent, command, exe, cwd, username, ppid, create_time, num_threads, num_fds, rss, vms, read_bytes, write_bytes FROM processes WHERE client_id=? ORDER BY cpu_percent DESC`, clientID)
	if err != nil {
		return nil
	}
	defer rows.Close()

	var procs []models.Process
	for rows.Next() {
		var p models.Process
		rows.Scan(&p.PID, &p.Name, &p.Status, &p.CPU, &p.Memory, &p.Command, &p.Exe, &p.Cwd, &p.Username, &p.Ppid, &p.CreateTime, &p.NumThreads, &p.NumFDs, &p.RSS, &p.VMS, &p.ReadBytes, &p.WriteBytes)
		procs = append(procs, p)
	}
	return procs
}

func (s *Store) GetProcess(clientID string, pid int32) (*models.Process, bool) {
	p := &models.Process{}
	err := s.db.QueryRow(`SELECT pid, name, status, cpu_percent, memory_percent, command, exe, cwd, username, ppid, create_time, num_threads, num_fds, rss, vms, read_bytes, write_bytes FROM processes WHERE client_id=? AND pid=?`, clientID, pid).
		Scan(&p.PID, &p.Name, &p.Status, &p.CPU, &p.Memory, &p.Command, &p.Exe, &p.Cwd, &p.Username, &p.Ppid, &p.CreateTime, &p.NumThreads, &p.NumFDs, &p.RSS, &p.VMS, &p.ReadBytes, &p.WriteBytes)
	if err != nil {
		return nil, false
	}
	return p, true
}

func (s *Store) GetApplication(clientID string, name string) (*models.Application, bool) {
	a := &models.Application{}
	var signedByJSON string
	err := s.db.QueryRow(`SELECT name, version, install_date, publisher, path, arch_kind, last_modified, signed_by FROM applications WHERE client_id=? AND name=?`, clientID, name).
		Scan(&a.Name, &a.Version, &a.InstallDate, &a.Publisher, &a.Path, &a.ArchKind, &a.LastModified, &signedByJSON)
	if err != nil {
		return nil, false
	}
	if signedByJSON != "" && signedByJSON != "null" {
		json.Unmarshal([]byte(signedByJSON), &a.SignedBy)
	}
	return a, true
}

func (s *Store) ListClients() []*models.Client {
	rows, err := s.db.Query(`SELECT id, hostname, os, arch, internal_ip, external_ip, version, status, data_hash, last_heartbeat, registered_at FROM clients ORDER BY hostname`)
	if err != nil {
		return nil
	}
	defer rows.Close()

	var clients []*models.Client
	for rows.Next() {
		c := &models.Client{}
		rows.Scan(&c.ID, &c.Hostname, &c.OS, &c.Arch, &c.InternalIP, &c.ExternalIP, &c.Version, &c.Status, &c.DataHash, &c.LastHeartbeat, &c.RegisteredAt)
		clients = append(clients, c)
	}
	return clients
}

func (s *Store) DeregisterClient(id string) {
	s.db.Exec(`DELETE FROM clients WHERE id=?`, id)
}

func (s *Store) notifyCommandComplete(commandID string) {
	s.cmdWaitersMu.Lock()
	waiters := s.cmdWaiters[commandID]
	delete(s.cmdWaiters, commandID)
	s.cmdWaitersMu.Unlock()
	for _, ch := range waiters {
		close(ch)
	}
}

func (s *Store) WaitForCommand(commandID string, timeout time.Duration) (*models.Command, bool) {
	ch := make(chan struct{}, 1)
	s.cmdWaitersMu.Lock()
	s.cmdWaiters[commandID] = append(s.cmdWaiters[commandID], ch)
	s.cmdWaitersMu.Unlock()

	select {
	case <-ch:
		cmd, ok := s.GetCommand(commandID)
		return cmd, ok
	case <-time.After(timeout):
		s.cmdWaitersMu.Lock()
		waiters := s.cmdWaiters[commandID]
		for i, w := range waiters {
			if w == ch {
				s.cmdWaiters[commandID] = append(waiters[:i], waiters[i+1:]...)
				break
			}
		}
		if len(s.cmdWaiters[commandID]) == 0 {
			delete(s.cmdWaiters, commandID)
		}
		s.cmdWaitersMu.Unlock()
		return s.GetCommand(commandID)
	}
}

func (s *Store) MarkStaleClients() {
	cutoff := time.Now().Add(-offlineThreshold)
	s.db.Exec(`UPDATE clients SET status='offline' WHERE last_heartbeat < ? AND status='online'`, cutoff)
}

func (s *Store) CreateCommand(clientID string, req models.CommandRequest) *models.Command {
	timeout := defaultCommandTimeout
	if req.Timeout > 0 {
		timeout = time.Duration(req.Timeout) * time.Second
	}

	cmd := &models.Command{
		ID:        generateUUID(),
		ClientID:  clientID,
		Type:      req.Type,
		Payload:   req.Payload,
		Status:    "pending",
		CreatedAt:  time.Now(),
		Timeout:   timeout,
	}

	payloadJSON, _ := json.Marshal(req.Payload)
	_, err := s.db.Exec(`INSERT INTO commands (id, client_id, type, payload, status, timeout_seconds, created_at) VALUES (?, ?, ?, ?, ?, ?, ?)`,
		cmd.ID, cmd.ClientID, cmd.Type, string(payloadJSON), cmd.Status, int(timeout.Seconds()), cmd.CreatedAt)
	if err != nil {
		log.Printf("create command db error: %v", err)
	}

	return cmd
}

func (s *Store) AckCommand(commandID string) (*models.Command, bool) {
	now := time.Now()
	res, err := s.db.Exec(`UPDATE commands SET status='acknowledged', acknowledged_at=? WHERE id=? AND status='pending'`, now, commandID)
	if err != nil {
		return nil, false
	}
	rows, _ := res.RowsAffected()
	if rows == 0 {
		return nil, false
	}

	cmd, ok := s.GetCommand(commandID)
	if !ok {
		return nil, false
	}
	return cmd, true
}

func (s *Store) ExpireCommands() {
	now := time.Now()
	rows, err := s.db.Query(`SELECT id FROM commands WHERE status IN ('pending', 'acknowledged') AND datetime(created_at, '+' || timeout_seconds || ' seconds') < ?`, now)
	if err != nil {
		return
	}
	defer rows.Close()

	var expired []string
	for rows.Next() {
		var id string
		rows.Scan(&id)
		expired = append(expired, id)
	}

	for _, id := range expired {
		s.db.Exec(`UPDATE commands SET status='expired', error='command timed out', completed_at=? WHERE id=?`, now, id)
		s.notifyCommandComplete(id)
	}
}

func (s *Store) GetPendingCommands(clientID string) []models.Command {
	rows, err := s.db.Query(`SELECT id, client_id, type, payload, status, timeout_seconds, created_at FROM commands WHERE client_id=? AND status='pending' ORDER BY created_at ASC`, clientID)
	if err != nil {
		return nil
	}
	defer rows.Close()

	var cmds []models.Command
	for rows.Next() {
		cmd := models.Command{}
		var payloadJSON string
		var timeoutSec int
		rows.Scan(&cmd.ID, &cmd.ClientID, &cmd.Type, &payloadJSON, &cmd.Status, &timeoutSec, &cmd.CreatedAt)
		json.Unmarshal([]byte(payloadJSON), &cmd.Payload)
		cmd.Timeout = time.Duration(timeoutSec) * time.Second
		cmds = append(cmds, cmd)
	}
	return cmds
}

func (s *Store) CompleteCommand(result models.CommandResult) (*models.Command, bool) {
	cmd := &models.Command{}
	var payloadJSON string
	var timeoutSec int
	err := s.db.QueryRow(`SELECT id, client_id, type, payload, status, timeout_seconds, created_at FROM commands WHERE id=?`, result.CommandID).
		Scan(&cmd.ID, &cmd.ClientID, &cmd.Type, &payloadJSON, &cmd.Status, &timeoutSec, &cmd.CreatedAt)
	if err != nil {
		return nil, false
	}

	json.Unmarshal([]byte(payloadJSON), &cmd.Payload)
	cmd.Timeout = time.Duration(timeoutSec) * time.Second

	status := "completed"
	if result.Error != "" {
		status = "failed"
	}

	resultJSON, _ := json.Marshal(result.Result)
	now := time.Now()
	s.db.Exec(`UPDATE commands SET status=?, result=?, error=?, completed_at=? WHERE id=?`, status, string(resultJSON), result.Error, now, result.CommandID)

	cmd.Status = status
	cmd.Result = result.Result
	cmd.Error = result.Error
	cmd.CompletedAt = &now
	s.notifyCommandComplete(result.CommandID)
	return cmd, true
}

func (s *Store) GetCommand(id string) (*models.Command, bool) {
	cmd := &models.Command{}
	var payloadJSON string
	var timeoutSec int
	var acknowledgedAt, completedAt sql.NullTime
	err := s.db.QueryRow(`SELECT id, client_id, type, payload, status, timeout_seconds, created_at, acknowledged_at, completed_at, error FROM commands WHERE id=?`, id).
		Scan(&cmd.ID, &cmd.ClientID, &cmd.Type, &payloadJSON, &cmd.Status, &timeoutSec, &cmd.CreatedAt, &acknowledgedAt, &completedAt, &cmd.Error)
	if err != nil {
		return nil, false
	}

	json.Unmarshal([]byte(payloadJSON), &cmd.Payload)
	cmd.Timeout = time.Duration(timeoutSec) * time.Second

	if acknowledgedAt.Valid {
		cmd.AcknowledgedAt = &acknowledgedAt.Time
	}
	if completedAt.Valid {
		cmd.CompletedAt = &completedAt.Time
	}

	var resultJSON string
	err = s.db.QueryRow(`SELECT result FROM commands WHERE id=?`, id).Scan(&resultJSON)
	if err == nil && resultJSON != "" {
		var result any
		json.Unmarshal([]byte(resultJSON), &result)
		cmd.Result = result
	}

	return cmd, true
}

func (s *Store) GetClientCommands(clientID string) []*models.Command {
	rows, err := s.db.Query(`SELECT id, client_id, type, payload, status, timeout_seconds, created_at, acknowledged_at, completed_at, error FROM commands WHERE client_id=? ORDER BY created_at DESC LIMIT 100`, clientID)
	if err != nil {
		return nil
	}
	defer rows.Close()

	var cmds []*models.Command
	for rows.Next() {
		cmd := &models.Command{}
		var payloadJSON string
		var timeoutSec int
		var acknowledgedAt, completedAt sql.NullTime
		rows.Scan(&cmd.ID, &cmd.ClientID, &cmd.Type, &payloadJSON, &cmd.Status, &timeoutSec, &cmd.CreatedAt, &acknowledgedAt, &completedAt, &cmd.Error)
		json.Unmarshal([]byte(payloadJSON), &cmd.Payload)
		cmd.Timeout = time.Duration(timeoutSec) * time.Second
		if acknowledgedAt.Valid {
			cmd.AcknowledgedAt = &acknowledgedAt.Time
		}
		if completedAt.Valid {
			cmd.CompletedAt = &completedAt.Time
		}
		cmds = append(cmds, cmd)
	}
	return cmds
}

func (s *Store) DashboardStats() *models.DashboardStats {
	stats := &models.DashboardStats{
		OSDistribution:      make(map[string]int),
		ArchDistribution:    make(map[string]int),
		StatusDistribution: make(map[string]int),
	}

	clients := s.ListClients()
	s.MarkStaleClients()

	var totalCPU, totalMem, totalDisk float64
	now := time.Now()

	for _, c := range clients {
		stats.TotalClients++
		status := c.Status
		if now.Sub(c.LastHeartbeat) > offlineThreshold {
			status = "offline"
		}
		stats.StatusDistribution[status]++

		if status == "online" {
			stats.OnlineClients++
		} else {
			stats.OfflineClients++
		}

		stats.OSDistribution[c.OS]++
		stats.ArchDistribution[c.Arch]++

		sysStats := s.getSystemStats(c.ID)
		summary := models.ClientSummary{
			ID:            c.ID,
			Hostname:      c.Hostname,
			OS:            c.OS,
			Arch:          c.Arch,
			InternalIP:    c.InternalIP,
			ExternalIP:    c.ExternalIP,
			Status:        status,
			LastHeartbeat: c.LastHeartbeat,
		}
		if sysStats != nil {
			totalCPU += sysStats.CPUPercent
			totalMem += sysStats.MemoryPercent
			totalDisk += sysStats.DiskPercent
			summary.CPUPercent = sysStats.CPUPercent
			summary.MemoryPercent = sysStats.MemoryPercent
		}

		stats.Clients = append(stats.Clients, summary)
	}

	if stats.TotalClients > 0 {
		stats.AvgCPU = totalCPU / float64(stats.TotalClients)
		stats.AvgMemory = totalMem / float64(stats.TotalClients)
		stats.AvgDisk = totalDisk / float64(stats.TotalClients)
	}

	return stats
}

func (s *Store) StaleChecker() {
	ticker := time.NewTicker(10 * time.Second)
	for range ticker.C {
		s.MarkStaleClients()
		s.ExpireCommands()
	}
}

func generateUUID() string {
	b := make([]byte, 16)
	rand.Read(b)
	b[6] = (b[6] & 0x0f) | 0x40
	b[8] = (b[8] & 0x3f) | 0x80
	return fmt.Sprintf("%x-%x-%x-%x-%x", b[0:4], b[4:6], b[6:8], b[8:10], b[10:16])
}

func generateToken() string {
	b := make([]byte, 32)
	rand.Read(b)
	return "cpt_" + hex.EncodeToString(b)
}

func (s *Store) GetUserByEmail(email string) (*models.User, bool) {
	u := &models.User{}
	err := s.db.QueryRow(`SELECT id, email, name, role, created_at FROM users WHERE email=?`, email).
		Scan(&u.ID, &u.Email, &u.Name, &u.Role, &u.CreatedAt)
	if err != nil {
		return nil, false
	}
	return u, true
}

func (s *Store) GetUser(id string) (*models.User, bool) {
	u := &models.User{}
	err := s.db.QueryRow(`SELECT id, email, name, role, created_at FROM users WHERE id=?`, id).
		Scan(&u.ID, &u.Email, &u.Name, &u.Role, &u.CreatedAt)
	if err != nil {
		return nil, false
	}
	return u, true
}

func (s *Store) ListUsers() []models.User {
	rows, err := s.db.Query(`SELECT id, email, name, role, created_at FROM users ORDER BY created_at`)
	if err != nil {
		return nil
	}
	defer rows.Close()
	var users []models.User
	for rows.Next() {
		var u models.User
		rows.Scan(&u.ID, &u.Email, &u.Name, &u.Role, &u.CreatedAt)
		users = append(users, u)
	}
	if users == nil {
		users = []models.User{}
	}
	return users
}

func (s *Store) CreateUser(email, name, role string) (*models.User, error) {
	id := generateUUID()
	now := time.Now()
	_, err := s.db.Exec(`INSERT INTO users (id, email, name, role, created_at) VALUES (?, ?, ?, ?, ?)`, id, email, name, role, now)
	if err != nil {
		return nil, err
	}
	return &models.User{ID: id, Email: email, Name: name, Role: role, CreatedAt: now}, nil
}

func (s *Store) UpdateUserRole(id, role string) bool {
	res, err := s.db.Exec(`UPDATE users SET role=? WHERE id=?`, role, id)
	if err != nil {
		return false
	}
	n, _ := res.RowsAffected()
	return n > 0
}

func (s *Store) DeleteUser(id string) bool {
	res, err := s.db.Exec(`DELETE FROM users WHERE id=?`, id)
	if err != nil {
		return false
	}
	n, _ := res.RowsAffected()
	return n > 0
}

func (s *Store) EnsureAdminExists(adminEmail string) {
	if adminEmail == "" {
		return
	}
	u, exists := s.GetUserByEmail(adminEmail)
	if exists {
		if u.Role != "admin" {
			s.UpdateUserRole(u.ID, "admin")
			log.Printf("Updated existing user %s to admin role", adminEmail)
		}
		return
	}
	_, err := s.CreateUser(adminEmail, "", "admin")
	if err != nil {
		log.Printf("Failed to create admin user: %v", err)
	} else {
		log.Printf("Created admin user: %s", adminEmail)
	}
}

func (s *Store) GetUserByEmailForAuth(email string) (id string, role string, exists bool) {
	u, ok := s.GetUserByEmail(email)
	if !ok {
		return "", "", false
	}
	return u.ID, u.Role, true
}

func (s *Store) AutoCreateUser(email, name string) string {
	u, err := s.CreateUser(email, name, "viewer")
	if err != nil {
		log.Printf("Failed to auto-create user %s: %v", email, err)
		return ""
	}
	log.Printf("Auto-created user: %s (role=viewer)", email)
	return u.Role
}

func (s *Store) AutoCreateUserWithRole(email, name, role string) {
	_, err := s.CreateUser(email, name, role)
	if err != nil {
		log.Printf("Failed to auto-create user %s with role %s: %v", email, role, err)
		return
	}
	log.Printf("Auto-created user: %s (role=%s)", email, role)
}

func (s *Store) SyncUserRole(email, role string) {
	u, ok := s.GetUserByEmail(email)
	if !ok {
		return
	}
	if u.Role != role {
		s.UpdateUserRole(u.ID, role)
		log.Printf("Synced role for %s: %s -> %s", email, u.Role, role)
	}
}
