package handlers

import (
	"encoding/json"
	"fmt"
	"net/http"
	"time"

	"github.com/go-chi/chi/v5"
	"tangled.sh/dunkirk.sh/arbitrage/controlplane/internal/models"
	"tangled.sh/dunkirk.sh/arbitrage/controlplane/internal/store"
	"tangled.sh/dunkirk.sh/arbitrage/controlplane/internal/ws"
)

type API struct {
	store *store.Store
	hub   *ws.Hub
}

func NewAPI(s *store.Store, h *ws.Hub) *API {
	return &API{store: s, hub: h}
}

func (a *API) RegisterRoutes(r chi.Router) {
	r.Post("/api/register", a.RegisterClient)

	r.Group(func(r chi.Router) {
		r.Use(a.ClientAuth)
		r.Post("/api/heartbeat", a.Heartbeat)
		r.Post("/api/sync", a.FullSync)
		r.Post("/api/commands/ack", a.AckCommand)
		r.Post("/api/commands/result", a.ReportCommandResult)
	})

	r.Get("/api/clients", a.ListClients)
	r.Get("/api/clients/{id}", a.GetClient)
	r.Delete("/api/clients/{id}", a.DeregisterClient)
	r.Get("/api/clients/{id}/apps", a.GetClientApps)
	r.Get("/api/clients/{id}/apps/{name}", a.GetClientApp)
	r.Get("/api/clients/{id}/processes", a.GetClientProcesses)
	r.Get("/api/clients/{id}/processes/{pid}", a.GetClientProcess)
	r.Get("/api/clients/{id}/stats", a.GetClientStats)
	r.Get("/api/clients/{id}/osinfo", a.GetClientOSInfo)
	r.Post("/api/clients/{id}/commands", a.SendCommand)
	r.Post("/api/clients/{id}/exec", a.ExecSync)
	r.Get("/api/clients/{id}/commands", a.GetClientCommands)
	r.Get("/api/commands/{commandID}", a.GetCommand)
	r.Get("/api/dashboard", a.GetDashboard)
}

func (a *API) ClientAuth(next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		id, token, ok := r.BasicAuth()
		if !ok {
			w.Header().Set("WWW-Authenticate", `Basic realm="controlplane"`)
			http.Error(w, "authentication required", http.StatusUnauthorized)
			return
		}

		if !a.store.AuthenticateClient(id, token) {
			http.Error(w, "invalid credentials", http.StatusUnauthorized)
			return
		}

		r.SetBasicAuth(id, token)
		next.ServeHTTP(w, r)
	})
}

func getClientID(r *http.Request) string {
	id, _, _ := r.BasicAuth()
	return id
}

// RegisterClient godoc
// @Summary      Register a new client
// @Description  Auto-registers a remote client and returns UUID + token credentials
// @Tags         clients
// @Accept       json
// @Produce      json
// @Param        body  body      models.RegisterRequest   true  "Client registration info"
// @Success      201   {object}  models.RegisterResponse
// @Failure      400   {string}  string  "invalid request body"
// @Failure      500   {string}  string  "registration failed"
// @Router       /api/register [post]
func (a *API) RegisterClient(w http.ResponseWriter, r *http.Request) {
	var req models.RegisterRequest
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		http.Error(w, "invalid request body", http.StatusBadRequest)
		return
	}
	if req.Hostname == "" {
		http.Error(w, "hostname required", http.StatusBadRequest)
		return
	}

	resp := a.store.RegisterClient(req)
	if resp == nil {
		http.Error(w, "registration failed", http.StatusInternalServerError)
		return
	}

	a.hub.Broadcast("client_registered", resp)

	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(http.StatusCreated)
	json.NewEncoder(w).Encode(resp)
}

// Heartbeat godoc
// @Summary      Send client heartbeat
// @Description  Sends a heartbeat with current system stats and data hash. Server responds with pending commands and whether a full sync is needed.
// @Tags         clients
// @Accept       json
// @Produce      json
// @Param        Authorization  header    string                   true  "Basic auth (client_id:token)"
// @Param        body           body     models.HeartbeatRequest  true  "Heartbeat data"
// @Success      200            {object} models.HeartbeatResponse
// @Failure      401            {string} string  "authentication required"
// @Failure      404            {string} string  "client not found"
// @Router       /api/heartbeat [post]
func (a *API) Heartbeat(w http.ResponseWriter, r *http.Request) {
	clientID := getClientID(r)

	var hb models.HeartbeatRequest
	if err := json.NewDecoder(r.Body).Decode(&hb); err != nil {
		http.Error(w, "invalid request body", http.StatusBadRequest)
		return
	}

	resp := a.store.UpdateHeartbeat(clientID, hb)
	if resp == nil {
		http.Error(w, "client not found", http.StatusNotFound)
		return
	}

	a.hub.Broadcast("client_heartbeat", map[string]string{"id": clientID, "status": "online"})

	w.Header().Set("Content-Type", "application/json")
	json.NewEncoder(w).Encode(resp)
}

// FullSync godoc
// @Summary      Full data sync
// @Description  Push full system data including applications and processes to the server
// @Tags         clients
// @Accept       json
// @Produce      json
// @Param        Authorization  header    string             true  "Basic auth (client_id:token)"
// @Param        body           body     models.SyncRequest  true  "Full sync data"
// @Success      200            {object} map[string]string
// @Failure      401            {string} string  "authentication required"
// @Failure      500            {string} string  "sync failed"
// @Router       /api/sync [post]
func (a *API) FullSync(w http.ResponseWriter, r *http.Request) {
	clientID := getClientID(r)

	var sync models.SyncRequest
	if err := json.NewDecoder(r.Body).Decode(&sync); err != nil {
		http.Error(w, "invalid request body", http.StatusBadRequest)
		return
	}

	if !a.store.FullSync(clientID, sync) {
		http.Error(w, "sync failed", http.StatusInternalServerError)
		return
	}

	client, _ := a.store.GetClient(clientID)
	a.hub.Broadcast("client_data_updated", client)

	w.Header().Set("Content-Type", "application/json")
	json.NewEncoder(w).Encode(map[string]string{"status": "ok"})
}

// ListClients godoc
// @Summary      List all clients
// @Description  Returns a list of all registered clients
// @Tags         clients
// @Produce      json
// @Success      200  {array}   models.Client
// @Router       /api/clients [get]
func (a *API) ListClients(w http.ResponseWriter, r *http.Request) {
	clients := a.store.ListClients()
	w.Header().Set("Content-Type", "application/json")
	json.NewEncoder(w).Encode(clients)
}

// GetClient godoc
// @Summary      Get client details
// @Description  Returns full client details including stats, OS info, apps, and processes
// @Tags         clients
// @Produce      json
// @Param        id   path      string  true  "Client ID"
// @Success      200  {object}  models.Client
// @Failure      404  {string}  string  "client not found"
// @Router       /api/clients/{id} [get]
func (a *API) GetClient(w http.ResponseWriter, r *http.Request) {
	clientID := chi.URLParam(r, "id")

	client, ok := a.store.GetClient(clientID)
	if !ok {
		http.Error(w, "client not found", http.StatusNotFound)
		return
	}

	w.Header().Set("Content-Type", "application/json")
	json.NewEncoder(w).Encode(client)
}

// DeregisterClient godoc
// @Summary      Deregister a client
// @Description  Removes a client and all its associated data
// @Tags         clients
// @Produce      json
// @Param        id   path      string  true  "Client ID"
// @Success      200  {object}  map[string]string
// @Failure      404  {string}  string  "client not found"
// @Router       /api/clients/{id} [delete]
func (a *API) DeregisterClient(w http.ResponseWriter, r *http.Request) {
	clientID := chi.URLParam(r, "id")

	_, ok := a.store.GetClient(clientID)
	if !ok {
		http.Error(w, "client not found", http.StatusNotFound)
		return
	}

	a.store.DeregisterClient(clientID)
	a.hub.Broadcast("client_deregistered", map[string]string{"id": clientID})

	w.Header().Set("Content-Type", "application/json")
	json.NewEncoder(w).Encode(map[string]string{"status": "ok"})
}

// GetClientApps godoc
// @Summary      Get client applications
// @Description  Returns list of installed applications for a client
// @Tags         clients
// @Produce      json
// @Param        id   path      string  true  "Client ID"
// @Success      200  {array}   models.Application
// @Failure      404  {string}  string  "client not found"
// @Router       /api/clients/{id}/apps [get]
func (a *API) GetClientApps(w http.ResponseWriter, r *http.Request) {
	clientID := chi.URLParam(r, "id")

	client, ok := a.store.GetClient(clientID)
	if !ok {
		http.Error(w, "client not found", http.StatusNotFound)
		return
	}

	w.Header().Set("Content-Type", "application/json")
	json.NewEncoder(w).Encode(client.Applications)
}

// GetClientProcesses godoc
// @Summary      Get client processes
// @Description  Returns list of running processes for a client
// @Tags         clients
// @Produce      json
// @Param        id   path      string  true  "Client ID"
// @Success      200  {array}   models.Process
// @Failure      404  {string}  string  "client not found"
// @Router       /api/clients/{id}/processes [get]
func (a *API) GetClientProcesses(w http.ResponseWriter, r *http.Request) {
	clientID := chi.URLParam(r, "id")

	client, ok := a.store.GetClient(clientID)
	if !ok {
		http.Error(w, "client not found", http.StatusNotFound)
		return
	}

	w.Header().Set("Content-Type", "application/json")
	json.NewEncoder(w).Encode(client.Processes)
}

// GetClientApp godoc
// @Summary      Get a specific application
// @Description  Returns detailed info for a single application by name
// @Tags         clients
// @Produce      json
// @Param        id     path      string  true  "Client ID"
// @Param        name   path      string  true  "Application name"
// @Success      200    {object}  models.Application
// @Failure      404    {string}  string  "not found"
// @Router       /api/clients/{id}/apps/{name} [get]
func (a *API) GetClientApp(w http.ResponseWriter, r *http.Request) {
	clientID := chi.URLParam(r, "id")
	name := chi.URLParam(r, "name")

	app, ok := a.store.GetApplication(clientID, name)
	if !ok {
		http.Error(w, "application not found", http.StatusNotFound)
		return
	}

	w.Header().Set("Content-Type", "application/json")
	json.NewEncoder(w).Encode(app)
}

// GetClientProcess godoc
// @Summary      Get a specific process
// @Description  Returns detailed info for a single process by PID
// @Tags         clients
// @Produce      json
// @Param        id     path      string  true  "Client ID"
// @Param        pid    path      int     true  "Process ID"
// @Success      200    {object}  models.Process
// @Failure      404    {string}  string  "not found"
// @Router       /api/clients/{id}/processes/{pid} [get]
func (a *API) GetClientProcess(w http.ResponseWriter, r *http.Request) {
	clientID := chi.URLParam(r, "id")
	pidStr := chi.URLParam(r, "pid")
	var pid int32
	fmt.Sscanf(pidStr, "%d", &pid)

	proc, ok := a.store.GetProcess(clientID, pid)
	if !ok {
		http.Error(w, "process not found", http.StatusNotFound)
		return
	}

	w.Header().Set("Content-Type", "application/json")
	json.NewEncoder(w).Encode(proc)
}

// GetClientStats godoc
// @Summary      Get client system stats
// @Description  Returns current system resource statistics for a client
// @Tags         clients
// @Produce      json
// @Param        id   path      string           true  "Client ID"
// @Success      200  {object}  models.SystemStats
// @Failure      404  {string}  string  "client not found or no stats"
// @Router       /api/clients/{id}/stats [get]
func (a *API) GetClientStats(w http.ResponseWriter, r *http.Request) {
	clientID := chi.URLParam(r, "id")

	client, ok := a.store.GetClient(clientID)
	if !ok {
		http.Error(w, "client not found", http.StatusNotFound)
		return
	}

	if client.SystemStats == nil {
		http.Error(w, "no stats available", http.StatusNotFound)
		return
	}

	w.Header().Set("Content-Type", "application/json")
	json.NewEncoder(w).Encode(client.SystemStats)
}

// GetClientOSInfo godoc
// @Summary      Get client OS info
// @Description  Returns operating system information for a client
// @Tags         clients
// @Produce      json
// @Param        id   path      string       true  "Client ID"
// @Success      200  {object}  models.OSInfo
// @Failure      404  {string}  string  "client not found or no os info"
// @Router       /api/clients/{id}/osinfo [get]
func (a *API) GetClientOSInfo(w http.ResponseWriter, r *http.Request) {
	clientID := chi.URLParam(r, "id")

	client, ok := a.store.GetClient(clientID)
	if !ok {
		http.Error(w, "client not found", http.StatusNotFound)
		return
	}

	if client.OSInfo == nil {
		http.Error(w, "no os info available", http.StatusNotFound)
		return
	}

	w.Header().Set("Content-Type", "application/json")
	json.NewEncoder(w).Encode(client.OSInfo)
}

// SendCommand godoc
// @Summary      Send command to client
// @Description  Dispatches a command to a specific client. The client will receive it on next heartbeat.
// @Tags         commands
// @Accept       json
// @Produce      json
// @Param        id    path      string               true  "Client ID"
// @Param        body  body      models.CommandRequest true  "Command to send"
// @Success      200   {object}  models.Command
// @Failure      400   {string}  string  "invalid request"
// @Failure      404   {string}  string  "client not found"
// @Router       /api/clients/{id}/commands [post]
func (a *API) SendCommand(w http.ResponseWriter, r *http.Request) {
	clientID := chi.URLParam(r, "id")

	_, ok := a.store.GetClient(clientID)
	if !ok {
		http.Error(w, "client not found", http.StatusNotFound)
		return
	}

	var req models.CommandRequest
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		http.Error(w, "invalid request body", http.StatusBadRequest)
		return
	}
	if req.Type == "" {
		http.Error(w, "command type required", http.StatusBadRequest)
		return
	}

	cmd := a.store.CreateCommand(clientID, req)
	a.hub.Broadcast("command_created", cmd)

	w.Header().Set("Content-Type", "application/json")
	json.NewEncoder(w).Encode(cmd)
}

// ExecSync godoc
// @Summary      Execute command synchronously
// @Description  Sends a command to a client and waits up to 30s for the result. Returns the completed command with output.
// @Tags         commands
// @Accept       json
// @Produce      json
// @Param        id    path      string               true  "Client ID"
// @Param        body  body      models.CommandRequest true  "Command to execute"
// @Success      200   {object}  models.Command
// @Failure      400   {string}  string  "invalid request"
// @Failure      404   {string}  string  "client not found"
// @Failure      504   {string}  string  "command timed out"
// @Router       /api/clients/{id}/exec [post]
func (a *API) ExecSync(w http.ResponseWriter, r *http.Request) {
	clientID := chi.URLParam(r, "id")

	_, ok := a.store.GetClient(clientID)
	if !ok {
		http.Error(w, "client not found", http.StatusNotFound)
		return
	}

	var req models.CommandRequest
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		http.Error(w, "invalid request body", http.StatusBadRequest)
		return
	}
	if req.Type == "" {
		req.Type = "exec"
	}
	if req.Type == "exec" && req.Payload == nil {
		http.Error(w, "payload required for exec", http.StatusBadRequest)
		return
	}
	if req.Timeout <= 0 {
		req.Timeout = 30
	}

	cmd := a.store.CreateCommand(clientID, req)
	a.hub.Broadcast("command_created", cmd)

	waitTimeout := time.Duration(req.Timeout+15) * time.Second
	result, found := a.store.WaitForCommand(cmd.ID, waitTimeout)
	if !found {
		http.Error(w, "command not found", http.StatusNotFound)
		return
	}

	if result.Status == "pending" || result.Status == "acknowledged" {
		w.Header().Set("Content-Type", "application/json")
		w.WriteHeader(http.StatusGatewayTimeout)
		json.NewEncoder(w).Encode(result)
		return
	}

	w.Header().Set("Content-Type", "application/json")
	json.NewEncoder(w).Encode(result)
}

// GetClientCommands godoc
// @Summary      Get command history for client
// @Description  Returns up to 100 most recent commands for a client
// @Tags         commands
// @Produce      json
// @Param        id   path      string  true  "Client ID"
// @Success      200  {array}   models.Command
// @Router       /api/clients/{id}/commands [get]
func (a *API) GetClientCommands(w http.ResponseWriter, r *http.Request) {
	clientID := chi.URLParam(r, "id")

	cmds := a.store.GetClientCommands(clientID)
	w.Header().Set("Content-Type", "application/json")
	json.NewEncoder(w).Encode(cmds)
}

// GetCommand godoc
// @Summary      Get command status
// @Description  Returns the current status and result of a specific command
// @Tags         commands
// @Produce      json
// @Param        commandID  path      string  true  "Command ID"
// @Success      200       {object}  models.Command
// @Failure      404       {string}  string  "command not found"
// @Router       /api/commands/{commandID} [get]
func (a *API) GetCommand(w http.ResponseWriter, r *http.Request) {
	commandID := chi.URLParam(r, "commandID")

	cmd, ok := a.store.GetCommand(commandID)
	if !ok {
		http.Error(w, "command not found", http.StatusNotFound)
		return
	}

	w.Header().Set("Content-Type", "application/json")
	json.NewEncoder(w).Encode(cmd)
}

// AckCommand godoc
// @Summary      Acknowledge a command
// @Description  Client acknowledges receipt of a command. Transitions status from pending to acknowledged.
// @Tags         commands
// @Accept       json
// @Produce      json
// @Param        Authorization  header    string             true  "Basic auth (client_id:token)"
// @Param        body           body     models.CommandAck  true  "Command acknowledgment"
// @Success      200            {object}  models.Command
// @Failure      401            {string}  string  "authentication required"
// @Failure      404            {string}  string  "command not found"
// @Router       /api/commands/ack [post]
func (a *API) AckCommand(w http.ResponseWriter, r *http.Request) {
	var ack models.CommandAck
	if err := json.NewDecoder(r.Body).Decode(&ack); err != nil {
		http.Error(w, "invalid request body", http.StatusBadRequest)
		return
	}

	cmd, ok := a.store.AckCommand(ack.CommandID)
	if !ok {
		http.Error(w, "command not found or already acknowledged", http.StatusNotFound)
		return
	}

	a.hub.Broadcast("command_acknowledged", cmd)

	w.Header().Set("Content-Type", "application/json")
	json.NewEncoder(w).Encode(cmd)
}

// ReportCommandResult godoc
// @Summary      Report command result
// @Description  Client reports the result of executing a command. Transitions status to completed or failed.
// @Tags         commands
// @Accept       json
// @Produce      json
// @Param        Authorization  header    string                true  "Basic auth (client_id:token)"
// @Param        body           body     models.CommandResult  true  "Command result"
// @Success      200            {object}  models.Command
// @Failure      401            {string}  string  "authentication required"
// @Failure      404            {string}  string  "command not found"
// @Router       /api/commands/result [post]
func (a *API) ReportCommandResult(w http.ResponseWriter, r *http.Request) {
	var result models.CommandResult
	if err := json.NewDecoder(r.Body).Decode(&result); err != nil {
		http.Error(w, "invalid request body", http.StatusBadRequest)
		return
	}

	cmd, ok := a.store.CompleteCommand(result)
	if !ok {
		http.Error(w, "command not found", http.StatusNotFound)
		return
	}

	a.hub.Broadcast("command_completed", cmd)

	w.Header().Set("Content-Type", "application/json")
	json.NewEncoder(w).Encode(cmd)
}

// GetDashboard godoc
// @Summary      Get dashboard stats
// @Description  Returns aggregated statistics for the overview dashboard including OS/arch distribution and averages
// @Tags         dashboard
// @Produce      json
// @Success      200  {object}  models.DashboardStats
// @Router       /api/dashboard [get]
func (a *API) GetDashboard(w http.ResponseWriter, r *http.Request) {
	stats := a.store.DashboardStats()
	w.Header().Set("Content-Type", "application/json")
	json.NewEncoder(w).Encode(stats)
}
