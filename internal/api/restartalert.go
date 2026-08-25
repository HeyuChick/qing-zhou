package api

import (
	"context"
	"encoding/json"
	"fmt"
	"log"
	"net/http"
	"strconv"
	"strings"
	"time"

	"github.com/go-chi/chi/v5"

	"qingzhou/internal/sbctl"
	"qingzhou/internal/store"
	"qingzhou/internal/telegram"
)

// Node restart-loop watching.
//
// Every sing-box restart cuts all connections on that node. One restart per
// change is the price of a config update; a node restarting on pass after pass
// with nobody touching it is an outage that the panel would otherwise report as
// a series of successful deploys — which is exactly how a config-compare bug
// once cut every node's connections once a minute for a day without a single
// error anywhere.
//
// The cost of watching for it has to be nothing when nothing is wrong, so:
//
//   - The controller reports only restarts caused by the PERIODIC pass. An admin
//     saving an inbound restarts nodes on purpose and is never counted.
//   - The rebuild goroutine does one non-blocking channel send and nothing else.
//     No DB read, no settings lookup, no network, no lock it could wait on.
//   - All state lives in one goroutine (no mutex), as timestamps in memory.
//     Thresholds are read from the DB only when a restart actually happened.
//   - A row is written only when an episode opens or closes, and server_alerts
//     folds repeats into hits+1 by itself.
//   - Telegram delivery sits behind a second queue, so a hung API cannot slow
//     the watcher, let alone config deployment.

const (
	// restartEventQueue is deliberately larger than any plausible burst: one
	// event per node per pass, and a pass is a minute apart.
	restartEventQueue = 64
	// opsMessageQueue bounds pending Telegram sends. Full means a very stuck
	// bot; the alert is already in the panel, so the push is dropped rather
	// than allowed to pile up.
	opsMessageQueue = 32
	// restartHistoryMax caps per-node timestamps. Only the count within the
	// window matters, so remembering more would change no decision.
	restartHistoryMax = 64
	// testSendBudget is how long the test-message handler keeps starting new
	// deliveries. Sized so that one more send starting at the buzzer (tgSend
	// allows 12s) still finishes inside the server's 30s WriteTimeout.
	testSendBudget = 10 * time.Second
)

type restartEvent struct {
	serverID int64
	name     string
	at       time.Time
	// sweep asks for the idle check instead of recording a restart. It rides the
	// same channel so the tracker needs no lock.
	sweep bool
	// recovered is emitted only after an administrator-forced apply succeeds.
	// A quiet circuit is still open, so silence alone must never resolve it.
	recovered bool
	// tripped carries the controller's already-made decision. Count/window are
	// captured there so a concurrent settings edit cannot open a circuit under
	// one threshold while the Telegram watcher re-evaluates another.
	tripped   bool
	count     int
	windowSec int64
}

// restartTracker holds the recent restart times per node and which nodes are
// currently alerting. Pure bookkeeping with the clock passed in, so the policy
// is testable without goroutines, timers or a database.
type restartTracker struct {
	hist    map[int64][]int64
	names   map[int64]string
	alerted map[int64]bool
}

func newRestartTracker() *restartTracker {
	return &restartTracker{
		hist:    map[int64][]int64{},
		names:   map[int64]string{},
		alerted: map[int64]bool{},
	}
}

// record notes a restart and reports whether this node just crossed from
// "restarting" into "restarting in a loop". It fires once per episode: while a
// node stays over the threshold, the alert is already open and re-announcing it
// would only spam whoever is receiving.
func (t *restartTracker) record(serverID int64, name string, now, windowSec int64, threshold int) (fire bool, count int) {
	t.names[serverID] = name
	h := append(t.hist[serverID], now)
	cutoff := now - windowSec
	kept := h[:0]
	for _, ts := range h {
		if ts >= cutoff {
			kept = append(kept, ts)
		}
	}
	if len(kept) > restartHistoryMax {
		kept = kept[len(kept)-restartHistoryMax:]
	}
	t.hist[serverID] = kept
	count = len(kept)
	if threshold <= 0 || count < threshold || t.alerted[serverID] {
		return false, count
	}
	t.alerted[serverID] = true
	return true, count
}

// prune drops old pre-threshold samples. Alerted nodes are deliberately retained:
// once the controller opens its circuit it becomes quiet by design, and only a
// successful administrator-forced apply is proof of recovery.
func (t *restartTracker) prune(now, windowSec int64) {
	cutoff := now - windowSec
	for id, hist := range t.hist {
		if t.alerted[id] {
			continue
		}
		kept := hist[:0]
		for _, ts := range hist {
			if ts >= cutoff {
				kept = append(kept, ts)
			}
		}
		if len(kept) == 0 {
			delete(t.hist, id)
			delete(t.names, id)
		} else {
			t.hist[id] = kept
		}
	}
}

func (t *restartTracker) recover(serverID int64) {
	delete(t.alerted, serverID)
	delete(t.hist, serverID)
	delete(t.names, serverID)
}

func (t *restartTracker) markAlerted(serverID int64, name string) {
	t.names[serverID] = name
	t.alerted[serverID] = true
}

// unmarkAlerted preserves the timestamps so the ordinary restart sample queued
// immediately after a circuit transition can retry a failed durable alert.
func (t *restartTracker) unmarkAlerted(serverID int64) {
	delete(t.alerted, serverID)
}

func (t *restartTracker) name(serverID int64) string {
	if n := t.names[serverID]; n != "" {
		return n
	}
	return fmt.Sprintf("#%d", serverID)
}

// restartPolicy is the admin-configured shape of "too many restarts".
type restartPolicy struct {
	enabled   bool
	windowSec int64
	threshold int
}

func (a *API) restartPolicy() restartPolicy {
	p := restartPolicy{enabled: true, windowSec: 30 * 60, threshold: 5}
	if v, err := a.st.GetSetting("alert_restart_enabled"); err == nil && v != "" {
		p.enabled = v == "true" || v == "1"
	}
	if v, err := a.st.GetSettingInt64("alert_restart_window_min", 30); err == nil && v > 0 {
		p.windowSec = v * 60
	}
	if v, err := a.st.GetSettingInt64("alert_restart_count", 5); err == nil && v > 0 {
		p.threshold = int(v)
	}
	return p
}

// RestartCircuitPolicy exposes the same live settings to sbctl. Keeping one
// source of truth guarantees that the Telegram message and the actual circuit
// open on the same restart.
func (a *API) RestartCircuitPolicy() sbctl.RestartCircuitPolicy {
	p := a.restartPolicy()
	return sbctl.RestartCircuitPolicy{Enabled: p.enabled, Window: time.Duration(p.windowSec) * time.Second, Threshold: p.threshold}
}

// RestartCircuitOpen restores the latch from the unresolved alert after a panel
// restart. Acknowledging the alert does not silently re-enable automatic deploys.
func (a *API) RestartCircuitOpen(serverID int64) bool {
	open, err := a.st.IsAlertOpen(serverID, store.AlertRestartLoop)
	return err == nil && open
}

// NodeCircuitChanged receives non-blocking transition notifications from the
// controller. Opening is announced by the threshold-crossing restart event;
// closing needs its own event so recipients get an explicit recovery message.
func (a *API) NodeCircuitChanged(ev sbctl.RestartCircuitEvent) {
	if a.restartCh == nil {
		return
	}
	e := restartEvent{serverID: ev.ServerID, name: ev.Name, at: time.Now()}
	if ev.Open {
		e.tripped = true
		e.count = ev.Count
		e.windowSec = int64(ev.Window / time.Second)
	} else {
		e.recovered = true
	}
	select {
	case a.restartCh <- e:
	default:
	}
}

// NodeRestarted is the controller's restart observer. It runs on the rebuild
// goroutine, so it does exactly one thing: hand the event over. A full queue
// drops the event — losing one sample of a condition that repeats every minute
// costs nothing, while blocking config deployment on an alerting side-channel
// would be indefensible.
func (a *API) NodeRestarted(serverID int64, name string) {
	if a.restartCh == nil {
		return
	}
	select {
	case a.restartCh <- restartEvent{serverID: serverID, name: name, at: time.Now()}:
	default:
	}
}

// sweepRestartAlerts prunes old pre-threshold samples. Open circuits are not
// closed by silence: silence is their expected state, and only a successful
// manual apply proves recovery. Called from the existing maintenance tick.
func (a *API) sweepRestartAlerts() {
	if a.restartCh == nil {
		return
	}
	select {
	case a.restartCh <- restartEvent{at: time.Now(), sweep: true}:
	default:
	}
}

// StartRestartWatch runs the watcher and the Telegram sender until ctx ends.
func (a *API) StartRestartWatch(ctx context.Context) {
	go a.runOpsSender(ctx)
	go a.runRestartWatch(ctx)
}

func (a *API) runRestartWatch(ctx context.Context) {
	tracker := newRestartTracker()
	for {
		select {
		case <-ctx.Done():
			return
		case ev := <-a.restartCh:
			now := ev.at.Unix()
			policy := a.restartPolicy()
			if ev.tripped {
				minutes := ev.windowSec / 60
				if minutes < 1 {
					minutes = 1
				}
				if a.raiseRestartCircuit(ev.serverID, ev.name, ev.count, minutes, now) {
					tracker.markAlerted(ev.serverID, ev.name)
				}
				continue
			}
			if ev.recovered {
				wasOpen, _ := a.st.IsAlertOpen(ev.serverID, store.AlertRestartLoop)
				tracker.recover(ev.serverID)
				if err := a.st.ResolveAlert(ev.serverID, store.AlertRestartLoop); err != nil {
					log.Printf("restart watch: resolve recovered circuit for server %d: %v", ev.serverID, err)
					continue
				}
				if wasOpen {
					log.Printf("restart watch: server %d (%s) 人工下发成功，已解除熔断", ev.serverID, ev.name)
					a.queueOpsMessage(renderRestartCircuitRecovery(ev.name))
				}
				continue
			}
			if ev.sweep {
				tracker.prune(now, policy.windowSec)
				continue
			}
			if !policy.enabled {
				continue
			}
			fire, count := tracker.record(ev.serverID, ev.name, now, policy.windowSec, policy.threshold)
			if !fire {
				continue
			}
			minutes := policy.windowSec / 60
			name := tracker.name(ev.serverID)
			if !a.raiseRestartCircuit(ev.serverID, name, count, minutes, now) {
				tracker.unmarkAlerted(ev.serverID)
			}
		}
	}
}

func (a *API) raiseRestartCircuit(serverID int64, name string, count int, minutes, now int64) bool {
	created, err := a.st.InsertAlert(store.ServerAlert{
		ServerID: serverID,
		Type:     store.AlertRestartLoop,
		Message:  fmt.Sprintf("%d 分钟内自动重启 %d 次，已暂停该节点的周期性自动下发", minutes, count),
		Ts:       now,
	})
	if err != nil {
		log.Printf("restart watch: raise alert for server %d: %v", serverID, err)
		return false
	}
	log.Printf("restart watch: server %d (%s) 在 %d 分钟内自动重启 %d 次，已熔断并告警", serverID, name, minutes, count)
	if created {
		a.queueOpsMessage(renderRestartLoopAlert(name, count, minutes))
	}
	return true
}

// renderRestartLoopAlert is the Telegram body. It names the node and what the
// recipient should do; it deliberately carries no config, credential or user
// data, because a recipient need not be an administrator.
func renderRestartLoopAlert(name string, count int, minutes int64) string {
	return "🔴 <b>节点自动下发已熔断</b>\n\n" +
		"节点：<b>" + telegram.Escape(name) + "</b>\n" +
		fmt.Sprintf("最近 %d 分钟内自动重启 <b>%d</b> 次，且不是由后台操作触发的。\n\n", minutes, count) +
		"已暂停该节点的周期性配置下发；流量统计与探针上报继续运行。请检查后在面板中人工重新下发。\n\n" +
		"面板日志：\n" +
		"<code>journalctl -u qingzhou | grep 连接会断一次</code>"
}

func renderRestartCircuitRecovery(name string) string {
	return "✅ <b>节点自动下发已恢复</b>\n\n" +
		"节点：<b>" + telegram.Escape(name) + "</b>\n" +
		"管理员重新下发成功，重启熔断已解除，周期性配置同步恢复。"
}

func (a *API) queueOpsMessage(html string) {
	if a.opsCh == nil {
		return
	}
	select {
	case a.opsCh <- html:
	default:
		log.Printf("ops alert: 推送队列已满，本条只写入面板告警，未发送 Telegram")
	}
}

func (a *API) runOpsSender(ctx context.Context) {
	for {
		select {
		case <-ctx.Done():
			return
		case html := <-a.opsCh:
			a.deliverOpsMessage(html)
		}
	}
}

// deliverOpsMessage sends one alert to every current recipient. Failures are
// logged and dropped: the alert is already visible in the panel, and a retry
// loop against a bot that is down or a chat that blocked us would keep this
// goroutine busy for as long as the outage lasts.
func (a *API) deliverOpsMessage(html string) {
	chats, err := a.opsChatIDs()
	if err != nil {
		log.Printf("ops alert: 读取接收人失败: %v", err)
		return
	}
	if len(chats) == 0 {
		log.Printf("ops alert: 没有配置任何接收人，本条告警只出现在面板里")
		return
	}
	for _, chat := range chats {
		if err := a.tgSend(chat, html); err != nil {
			log.Printf("ops alert: 发送到 %d 失败: %v", chat, err)
		}
	}
}

// opsChatIDs resolves the current recipients: bound accounts flagged for ops
// alerts, plus any extra chats configured by hand (a group or channel the bot
// was added to). Deduplicated, because the same chat can appear both ways.
//
// Resolved per send rather than cached: whoever unbinds, gets banned or is
// unticked stops receiving with the next message, not eventually.
func (a *API) opsChatIDs() ([]int64, error) {
	recipients, err := a.st.ListOpsRecipients()
	if err != nil {
		return nil, err
	}
	seen := map[int64]bool{}
	var out []int64
	add := func(id int64) {
		if id == 0 || seen[id] {
			return
		}
		seen[id] = true
		out = append(out, id)
	}
	for _, r := range recipients {
		add(r.ChatID)
	}
	raw, _ := a.st.GetSetting("alert_ops_extra_chats")
	for _, id := range parseChatIDs(raw) {
		add(id)
	}
	return out, nil
}

// parseChatIDs reads the extra-chats setting: ids separated by commas,
// whitespace or newlines. Anything unparseable is skipped rather than failing
// the whole list — one typo must not silence every other recipient.
func parseChatIDs(raw string) []int64 {
	fields := strings.FieldsFunc(raw, func(r rune) bool {
		return r == ',' || r == '\n' || r == '\r' || r == ' ' || r == '\t' || r == ';' || r == '，'
	})
	var out []int64
	for _, f := range fields {
		if id, err := strconv.ParseInt(strings.TrimSpace(f), 10, 64); err == nil && id != 0 {
			out = append(out, id)
		}
	}
	return out
}

// ---- Admin API: who receives ops alerts ----

// handleListOpsRecipients returns every bound account with whether it receives
// ops alerts, plus the extra chats and how many chats would actually be reached
// right now. The last number is the point: a recipient list that quietly emptied
// out (someone unbound, someone got banned) otherwise looks configured while
// every alert goes nowhere.
func (a *API) handleListOpsRecipients(w http.ResponseWriter, r *http.Request) {
	candidates, err := a.st.ListOpsCandidates()
	if err != nil {
		fail(w, http.StatusInternalServerError, "读取已绑定账号失败")
		return
	}
	chats, err := a.opsChatIDs()
	if err != nil {
		fail(w, http.StatusInternalServerError, "读取接收人失败")
		return
	}
	extra, _ := a.st.GetSetting("alert_ops_extra_chats")
	if candidates == nil {
		candidates = []store.OpsCandidate{}
	}
	ok(w, J{
		"candidates":  candidates,
		"extra_chats": extra,
		"effective":   len(chats),
	})
}

// handleSetOpsRecipient turns ops alerts on or off for one account. Only an
// admin reaches this route, by design: the alerts name nodes and failure
// counts, so being on the list is something granted, never self-served.
func (a *API) handleSetOpsRecipient(w http.ResponseWriter, r *http.Request) {
	var in struct {
		On bool `json:"on"`
	}
	if err := json.NewDecoder(r.Body).Decode(&in); err != nil {
		fail(w, http.StatusBadRequest, "请求格式错误")
		return
	}
	bound, err := a.st.SetTelegramNotifyOps(atoi(chi.URLParam(r, "id")), in.On)
	if err != nil {
		fail(w, http.StatusInternalServerError, "保存失败")
		return
	}
	if !bound {
		fail(w, http.StatusBadRequest, "该账号没有绑定 Telegram，无法接收告警")
		return
	}
	ok(w, J{"on": in.On})
}

// handleTestOpsAlert sends a test message to the recipients as they stand.
//
// Delivered inline rather than through the queue, and reported per chat: an
// unverified extra chat id is exactly the thing this button exists to catch,
// and "queued" would tell the admin nothing about whether it arrived.
func (a *API) handleTestOpsAlert(w http.ResponseWriter, r *http.Request) {
	chats, err := a.opsChatIDs()
	if err != nil {
		fail(w, http.StatusInternalServerError, "读取接收人失败")
		return
	}
	if len(chats) == 0 {
		fail(w, http.StatusBadRequest, "还没有配置接收人")
		return
	}
	msg := "🔔 这是来自「" + telegram.Escape(a.siteName()) + "」的运维告警测试消息。\n\n" +
		"收到它说明节点异常时你会被通知到。"
	// One delivery can hang for the full Telegram timeout, and the HTTP server's
	// WriteTimeout is 30s (main.go): a handler that outlives it has its response
	// cut off, and the admin is told the test failed when it may well have been
	// delivered. So stop starting new sends once there is no longer room for one
	// more inside that budget, and report what was skipped instead.
	deadline := time.Now().Add(testSendBudget)
	var failures []string
	sent, skipped := 0, 0
	for _, chat := range chats {
		if time.Now().After(deadline) {
			skipped++
			continue
		}
		if err := a.tgSend(chat, msg); err != nil {
			failures = append(failures, fmt.Sprintf("%d: %s", chat, err.Error()))
			continue
		}
		sent++
	}
	if skipped > 0 {
		failures = append(failures, fmt.Sprintf("另有 %d 个聊天本次未测试（Telegram 响应太慢，再点一次继续）", skipped))
	}
	ok(w, J{"sent": sent, "failed": failures})
}
