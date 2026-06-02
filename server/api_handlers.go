package main

import (
	"encoding/json"
	"net/http"
	"strconv"
	"strings"
	"time"

	"github.com/gorilla/mux"

	"github.com/7c/pingmon/classes/arp"
	"github.com/7c/pingmon/classes/store"
)

// maxCompareIPs caps how many hosts a single multi-series request may fetch.
const maxCompareIPs = 25

// arpHandler serves the accumulated ARP scan results. The provider returns the
// current snapshot (the scanner's Entries method).
func arpHandler(provider func() []arp.Entry) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		entries := provider()
		writeJSON(w, http.StatusOK, map[string]interface{}{
			"count":   len(entries),
			"entries": entries,
		})
	}
}

// --- request helpers ---

// parseRange reads start/end RFC3339 query params, defaulting to the last 6h.
func parseRange(r *http.Request) (time.Time, time.Time, error) {
	q := r.URL.Query()
	end := time.Now().UTC()
	start := end.Add(-6 * time.Hour)

	if v := q.Get("end"); v != "" {
		t, err := time.Parse(time.RFC3339, v)
		if err != nil {
			return time.Time{}, time.Time{}, errBadTime("end")
		}
		end = t.UTC()
	}
	if v := q.Get("start"); v != "" {
		t, err := time.Parse(time.RFC3339, v)
		if err != nil {
			return time.Time{}, time.Time{}, errBadTime("start")
		}
		start = t.UTC()
	}
	if !start.Before(end) {
		return time.Time{}, time.Time{}, errStartAfterEnd
	}
	return start, end, nil
}

type apiError string

func (e apiError) Error() string { return string(e) }

const errStartAfterEnd apiError = "start must be before end"

func errBadTime(field string) error {
	return apiError("invalid " + field + " (expected RFC3339)")
}

// parseID extracts and parses a numeric path variable.
func parseID(r *http.Request, name string) (int64, error) {
	return strconv.ParseInt(mux.Vars(r)[name], 10, 64)
}

func badRequest(w http.ResponseWriter, msg string) {
	writeJSON(w, http.StatusBadRequest, map[string]interface{}{"success": false, "error": msg})
}

func ok(w http.ResponseWriter) {
	writeJSON(w, http.StatusOK, map[string]interface{}{"success": true})
}

// --- hosts (rich) ---

// handleListHostsFull returns every host with full metadata.
func (pm *PingerManager) handleListHostsFull(w http.ResponseWriter, r *http.Request) {
	hosts, err := pm.store.ListHostsFull()
	if err != nil {
		writeStoreError(w, err)
		return
	}
	writeJSON(w, http.StatusOK, map[string]interface{}{"hosts": hosts})
}

// handleGetHost returns a single host's full record.
func (pm *PingerManager) handleGetHost(w http.ResponseWriter, r *http.Request) {
	h, err := pm.store.GetHost(mux.Vars(r)["ip"])
	if err != nil {
		writeStoreError(w, err)
		return
	}
	writeJSON(w, http.StatusOK, h)
}

// handleUpdateConfig updates per-IP ping config and restarts the live pinger.
func (pm *PingerManager) handleUpdateConfig(w http.ResponseWriter, r *http.Request) {
	ip := mux.Vars(r)["ip"]
	var cfg store.HostConfig
	if err := json.NewDecoder(r.Body).Decode(&cfg); err != nil {
		badRequest(w, err.Error())
		return
	}
	cfg = clampConfig(cfg) // enforce the minimum ping interval
	if msg := validateConfig(cfg); msg != "" {
		badRequest(w, msg)
		return
	}
	if err := pm.UpdatePingerConfig(ip, cfg); err != nil {
		writeStoreError(w, err)
		return
	}
	ok(w)
}

// handleUpdateMeta updates display name, notes, and (optionally) tags.
func (pm *PingerManager) handleUpdateMeta(w http.ResponseWriter, r *http.Request) {
	ip := mux.Vars(r)["ip"]
	var req struct {
		DisplayName string    `json:"displayName"`
		Notes       string    `json:"notes"`
		Tags        *[]string `json:"tags"`
	}
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		badRequest(w, err.Error())
		return
	}
	if err := pm.store.UpdateHostMeta(ip, req.DisplayName, req.Notes); err != nil {
		writeStoreError(w, err)
		return
	}
	if req.Tags != nil {
		if err := pm.store.SetHostTags(ip, *req.Tags); err != nil {
			writeStoreError(w, err)
			return
		}
	}
	ok(w)
}

// handleUpdateAlerts updates the per-host alert thresholds.
func (pm *PingerManager) handleUpdateAlerts(w http.ResponseWriter, r *http.Request) {
	ip := mux.Vars(r)["ip"]
	var req struct {
		LatencyMs int     `json:"latencyMs"`
		LossPct   float64 `json:"lossPct"`
	}
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		badRequest(w, err.Error())
		return
	}
	if req.LatencyMs < 0 || req.LossPct < 0 || req.LossPct > 100 {
		badRequest(w, "latencyMs must be >= 0 and lossPct in [0,100]")
		return
	}
	if err := pm.store.UpdateHostAlerts(ip, req.LatencyMs, req.LossPct); err != nil {
		writeStoreError(w, err)
		return
	}
	ok(w)
}

// --- series (multi-IP overlay) ---

func (pm *PingerManager) handleSeries(w http.ResponseWriter, r *http.Request) {
	raw := r.URL.Query().Get("ips")
	if raw == "" {
		badRequest(w, "ips query param is required")
		return
	}
	ips := splitNonEmpty(raw, ",")
	if len(ips) == 0 {
		badRequest(w, "no ips provided")
		return
	}
	if len(ips) > maxCompareIPs {
		badRequest(w, "too many ips (max "+strconv.Itoa(maxCompareIPs)+")")
		return
	}
	start, end, err := parseRange(r)
	if err != nil {
		badRequest(w, err.Error())
		return
	}
	series, err := pm.store.GetMultiSeries(ips, start, end, r.URL.Query().Get("resolution"))
	if err != nil {
		writeStoreError(w, err)
		return
	}
	writeJSON(w, http.StatusOK, map[string]interface{}{"series": series})
}

// --- analytics ---

func (pm *PingerManager) handleAvailability(w http.ResponseWriter, r *http.Request) {
	start, end, err := parseRange(r)
	if err != nil {
		badRequest(w, err.Error())
		return
	}
	a, err := pm.store.GetAvailability(mux.Vars(r)["ip"], start, end)
	if err != nil {
		writeStoreError(w, err)
		return
	}
	writeJSON(w, http.StatusOK, a)
}

func (pm *PingerManager) handlePercentiles(w http.ResponseWriter, r *http.Request) {
	start, end, err := parseRange(r)
	if err != nil {
		badRequest(w, err.Error())
		return
	}
	p, err := pm.store.GetPercentiles(mux.Vars(r)["ip"], start, end)
	if err != nil {
		writeStoreError(w, err)
		return
	}
	writeJSON(w, http.StatusOK, p)
}

func (pm *PingerManager) handleOutages(w http.ResponseWriter, r *http.Request) {
	start, end, err := parseRange(r)
	if err != nil {
		badRequest(w, err.Error())
		return
	}
	minFails := 3
	if v := r.URL.Query().Get("minFails"); v != "" {
		if n, err := strconv.Atoi(v); err == nil {
			minFails = n
		}
	}
	ip := mux.Vars(r)["ip"]
	outages, err := pm.store.GetOutages(ip, start, end, minFails)
	if err != nil {
		writeStoreError(w, err)
		return
	}
	writeJSON(w, http.StatusOK, map[string]interface{}{"ip": ip, "outages": outages})
}

// --- status wall ---

func (pm *PingerManager) handleStatus(w http.ResponseWriter, r *http.Request) {
	n := 60
	if v := r.URL.Query().Get("n"); v != "" {
		if parsed, err := strconv.Atoi(v); err == nil && parsed > 0 {
			n = parsed
		}
	}
	statuses, err := pm.store.GetHostStatuses(n)
	if err != nil {
		writeStoreError(w, err)
		return
	}
	writeJSON(w, http.StatusOK, map[string]interface{}{
		"generatedAt": time.Now().UTC(),
		"hosts":       statuses,
	})
}

// --- groups ---

func (pm *PingerManager) handleListGroups(w http.ResponseWriter, r *http.Request) {
	groups, err := pm.store.ListGroups()
	if err != nil {
		writeStoreError(w, err)
		return
	}
	writeJSON(w, http.StatusOK, map[string]interface{}{"groups": groups})
}

func (pm *PingerManager) handleCreateGroup(w http.ResponseWriter, r *http.Request) {
	var req struct {
		Name        string `json:"name"`
		Color       string `json:"color"`
		Description string `json:"description"`
	}
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		badRequest(w, err.Error())
		return
	}
	if strings.TrimSpace(req.Name) == "" {
		badRequest(w, "name is required")
		return
	}
	g, err := pm.store.CreateGroup(req.Name, req.Color, req.Description)
	if err != nil {
		writeStoreError(w, err)
		return
	}
	writeJSON(w, http.StatusCreated, g)
}

func (pm *PingerManager) handleGetGroup(w http.ResponseWriter, r *http.Request) {
	id, err := parseID(r, "id")
	if err != nil {
		badRequest(w, "invalid group id")
		return
	}
	g, err := pm.store.GetGroup(id)
	if err != nil {
		writeStoreError(w, err)
		return
	}
	writeJSON(w, http.StatusOK, g)
}

func (pm *PingerManager) handleUpdateGroup(w http.ResponseWriter, r *http.Request) {
	id, err := parseID(r, "id")
	if err != nil {
		badRequest(w, "invalid group id")
		return
	}
	var req struct {
		Name        string `json:"name"`
		Color       string `json:"color"`
		Description string `json:"description"`
	}
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		badRequest(w, err.Error())
		return
	}
	if err := pm.store.UpdateGroup(id, req.Name, req.Color, req.Description); err != nil {
		writeStoreError(w, err)
		return
	}
	ok(w)
}

func (pm *PingerManager) handleDeleteGroup(w http.ResponseWriter, r *http.Request) {
	id, err := parseID(r, "id")
	if err != nil {
		badRequest(w, "invalid group id")
		return
	}
	if err := pm.store.DeleteGroup(id); err != nil {
		writeStoreError(w, err)
		return
	}
	ok(w)
}

func (pm *PingerManager) handleListGroupHosts(w http.ResponseWriter, r *http.Request) {
	id, err := parseID(r, "id")
	if err != nil {
		badRequest(w, "invalid group id")
		return
	}
	ips, err := pm.store.ListHostsInGroup(id)
	if err != nil {
		writeStoreError(w, err)
		return
	}
	writeJSON(w, http.StatusOK, map[string]interface{}{"ips": ips})
}

func (pm *PingerManager) handleAssignGroupHosts(w http.ResponseWriter, r *http.Request) {
	id, err := parseID(r, "id")
	if err != nil {
		badRequest(w, "invalid group id")
		return
	}
	var req struct {
		IP  string   `json:"ip"`
		IPs []string `json:"ips"`
	}
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		badRequest(w, err.Error())
		return
	}
	ips := req.IPs
	if req.IP != "" {
		ips = append(ips, req.IP)
	}
	for _, ip := range ips {
		if err := pm.store.AssignHostToGroup(ip, id); err != nil {
			writeStoreError(w, err)
			return
		}
	}
	ok(w)
}

func (pm *PingerManager) handleUnassignGroupHost(w http.ResponseWriter, r *http.Request) {
	id, err := parseID(r, "id")
	if err != nil {
		badRequest(w, "invalid group id")
		return
	}
	if err := pm.store.UnassignHostFromGroup(mux.Vars(r)["ip"], id); err != nil {
		writeStoreError(w, err)
		return
	}
	ok(w)
}

func (pm *PingerManager) handleGroupHealth(w http.ResponseWriter, r *http.Request) {
	id, err := parseID(r, "id")
	if err != nil {
		badRequest(w, "invalid group id")
		return
	}
	ips, err := pm.store.ListHostsInGroup(id)
	if err != nil {
		writeStoreError(w, err)
		return
	}
	members := make(map[string]bool, len(ips))
	for _, ip := range ips {
		members[ip] = true
	}

	statuses, err := pm.store.GetHostStatuses(60)
	if err != nil {
		writeStoreError(w, err)
		return
	}
	counts := map[string]int{
		store.StateUp: 0, store.StateDegraded: 0, store.StateDown: 0, store.StateUnknown: 0,
	}
	var lossSum, rttSum float64
	var rttCount int
	for _, st := range statuses {
		if !members[st.IP] {
			continue
		}
		counts[st.State]++
		lossSum += st.LossPct
		if st.AvgRTTNs != nil {
			rttSum += float64(*st.AvgRTTNs)
			rttCount++
		}
	}
	resp := map[string]interface{}{
		"groupId":    id,
		"counts":     counts,
		"avgLossPct": 0.0,
		"avgRttNs":   0.0,
	}
	if len(ips) > 0 {
		resp["avgLossPct"] = lossSum / float64(len(ips))
	}
	if rttCount > 0 {
		resp["avgRttNs"] = rttSum / float64(rttCount)
	}
	writeJSON(w, http.StatusOK, resp)
}

// --- annotations ---

func (pm *PingerManager) handleListAnnotations(w http.ResponseWriter, r *http.Request) {
	ip := mux.Vars(r)["ip"]
	start, end, err := parseRange(r)
	if err != nil {
		badRequest(w, err.Error())
		return
	}
	var types []string
	if t := r.URL.Query().Get("type"); t != "" {
		types = splitNonEmpty(t, ",")
	}
	anns, err := pm.store.ListAnnotations(ip, start, end, types)
	if err != nil {
		writeStoreError(w, err)
		return
	}
	writeJSON(w, http.StatusOK, map[string]interface{}{"annotations": anns})
}

func (pm *PingerManager) handleCreateAnnotation(w http.ResponseWriter, r *http.Request) {
	ip := mux.Vars(r)["ip"]
	var req annotationRequest
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		badRequest(w, err.Error())
		return
	}
	if req.StartTs == nil {
		badRequest(w, "startTs is required")
		return
	}
	a := store.Annotation{
		IP:      &ip,
		Type:    req.Type,
		Title:   req.Title,
		Text:    req.Text,
		Color:   req.Color,
		Author:  req.Author,
		StartTs: req.StartTs.UTC(),
		EndTs:   utcPtr(req.EndTs),
	}
	created, err := pm.store.CreateAnnotation(a)
	if err != nil {
		writeStoreError(w, err)
		return
	}
	writeJSON(w, http.StatusCreated, created)
}

func (pm *PingerManager) handleUpdateAnnotation(w http.ResponseWriter, r *http.Request) {
	id, err := parseID(r, "id")
	if err != nil {
		badRequest(w, "invalid annotation id")
		return
	}
	existing, err := pm.store.GetAnnotation(id)
	if err != nil {
		writeStoreError(w, err)
		return
	}
	var req annotationRequest
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		badRequest(w, err.Error())
		return
	}
	if req.Type != "" {
		existing.Type = req.Type
	}
	existing.Title = req.Title
	existing.Text = req.Text
	existing.Color = req.Color
	existing.Author = req.Author
	if req.StartTs != nil {
		existing.StartTs = req.StartTs.UTC()
	}
	existing.EndTs = utcPtr(req.EndTs)
	if err := pm.store.UpdateAnnotation(existing); err != nil {
		writeStoreError(w, err)
		return
	}
	ok(w)
}

func (pm *PingerManager) handleDeleteAnnotation(w http.ResponseWriter, r *http.Request) {
	id, err := parseID(r, "id")
	if err != nil {
		badRequest(w, "invalid annotation id")
		return
	}
	if err := pm.store.DeleteAnnotation(id); err != nil {
		writeStoreError(w, err)
		return
	}
	ok(w)
}

type annotationRequest struct {
	Type    string     `json:"type"`
	Title   string     `json:"title"`
	Text    string     `json:"text"`
	Color   string     `json:"color"`
	Author  string     `json:"author"`
	StartTs *time.Time `json:"startTs"`
	EndTs   *time.Time `json:"endTs"`
}

func utcPtr(t *time.Time) *time.Time {
	if t == nil {
		return nil
	}
	u := t.UTC()
	return &u
}

func splitNonEmpty(s, sep string) []string {
	parts := strings.Split(s, sep)
	out := make([]string, 0, len(parts))
	for _, p := range parts {
		if p = strings.TrimSpace(p); p != "" {
			out = append(out, p)
		}
	}
	return out
}
