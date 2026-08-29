package api

import (
	"fmt"
	"log"
	"time"

	"qingzhou/internal/store"
	"qingzhou/internal/telegram"
)

const (
	deviceNotifyExpiry  = "device_expiry"
	deviceNotifyTraffic = "device_traffic"
)

// sweepDeviceNotifications evaluates the two provider-facing device budgets:
// rental expiry and physical NIC traffic. Delivery cursors are persisted, so a
// restart inside a warning window cannot repeat an already delivered message.
func (a *API) sweepDeviceNotifications(now time.Time) {
	servers, err := a.st.ListServers()
	if err != nil {
		log.Printf("device notify: list servers: %v", err)
		return
	}
	asset := a.st.LocalAsset()
	servers = append(servers, &store.Server{
		ID: store.LocalNodeID, Name: store.LocalNodeName, ExpiryDate: asset.ExpiryDate,
		ExpiryNotifyEnabled: asset.ExpiryNotifyEnabled, ExpiryNotifyDays: asset.ExpiryNotifyDays,
		ExpiryNotifyMode: asset.ExpiryNotifyMode, ExpiryNotifyCount: asset.ExpiryNotifyCount,
		TrafficLimitBytes: asset.TrafficLimitBytes, TrafficResetDay: asset.TrafficResetDay,
		TrafficResetMinute: asset.TrafficResetMinute, TrafficAlertPercent: asset.TrafficAlertPercent,
	})

	starts := make(map[int64]int64, len(servers))
	for _, sv := range servers {
		starts[sv.ID] = store.TrafficCycleStart(now, sv.TrafficResetDay, sv.TrafficResetMinute).Unix()
	}
	usage, err := a.st.TrafficUsageForCycles(starts)
	if err != nil {
		log.Printf("device notify: aggregate traffic: %v", err)
		return
	}
	for _, sv := range servers {
		a.notifyDeviceExpiry(now, sv)
		a.notifyDeviceTraffic(now, sv, starts[sv.ID], usage[sv.ID])
	}
}

func (a *API) notifyDeviceExpiry(now time.Time, sv *store.Server) {
	if !sv.ExpiryNotifyEnabled || sv.ExpiryDate <= now.Unix() || sv.ExpiryNotifyDays < 1 {
		return
	}
	remaining := time.Unix(sv.ExpiryDate, 0).Sub(now)
	if remaining > time.Duration(sv.ExpiryNotifyDays)*24*time.Hour {
		return
	}
	cycleKey := fmt.Sprintf("expiry:%d", sv.ExpiryDate)
	state, err := a.st.DeviceNotifyState(sv.ID, deviceNotifyExpiry, cycleKey)
	if err != nil {
		log.Printf("device notify: read expiry cursor for %d: %v", sv.ID, err)
		return
	}
	today := now.Format("2006-01-02")
	if state.LastSentDay == today {
		return
	}
	if sv.ExpiryNotifyMode != "daily" && state.SentCount >= sv.ExpiryNotifyCount {
		return
	}
	leftHours := int64((remaining + time.Hour - 1) / time.Hour)
	left := fmt.Sprintf("%d 小时", leftHours)
	if leftHours >= 48 {
		left = fmt.Sprintf("%d 天", (leftHours+23)/24)
	}
	body := "⏰ <b>设备即将到期</b>\n\n" +
		"设备：<b>" + telegram.Escape(sv.Name) + "</b>\n" +
		"剩余：<b>" + left + "</b>\n" +
		"到期：" + time.Unix(sv.ExpiryDate, 0).In(now.Location()).Format("2006-01-02 15:04")
	if !a.sendDeviceOps(body) {
		return
	}
	if err := a.st.MarkDeviceNotifySent(sv.ID, deviceNotifyExpiry, cycleKey, today, now.Unix()); err != nil {
		log.Printf("device notify: save expiry cursor for %d: %v", sv.ID, err)
	}
}

func (a *API) notifyDeviceTraffic(now time.Time, sv *store.Server, cycleStart int64, usage store.ServerTrafficUsage) {
	if sv.TrafficLimitBytes <= 0 {
		_ = a.st.ResolveAlert(sv.ID, "traffic_threshold")
		return
	}
	percent := sv.TrafficAlertPercent
	if percent < 1 {
		percent = 80
	}
	reached := usage.SampleCount >= 2 && usage.Total*100 >= sv.TrafficLimitBytes*int64(percent)
	if !reached {
		_ = a.st.ResolveAlert(sv.ID, "traffic_threshold")
		return
	}
	msg := fmt.Sprintf("服务器「%s」本周期流量已达到 %d%%：%s / %s", sv.Name, percent,
		formatDeviceBytes(usage.Total), formatDeviceBytes(sv.TrafficLimitBytes))
	_, _ = a.st.InsertAlert(store.ServerAlert{ServerID: sv.ID, Type: "traffic_threshold", Message: msg, Ts: now.Unix()})
	cycleKey := fmt.Sprintf("cycle:%d", cycleStart)
	state, err := a.st.DeviceNotifyState(sv.ID, deviceNotifyTraffic, cycleKey)
	if err != nil || state.SentCount > 0 {
		return
	}
	nextReset := store.TrafficCycleNext(now, sv.TrafficResetDay, sv.TrafficResetMinute)
	body := "📊 <b>设备月流量告警</b>\n\n" +
		"设备：<b>" + telegram.Escape(sv.Name) + "</b>\n" +
		fmt.Sprintf("用量：<b>%s / %s</b>（阈值 %d%%）\n", formatDeviceBytes(usage.Total), formatDeviceBytes(sv.TrafficLimitBytes), percent) +
		"下次重置：" + nextReset.Format("2006-01-02 15:04")
	if !a.sendDeviceOps(body) {
		return
	}
	if err := a.st.MarkDeviceNotifySent(sv.ID, deviceNotifyTraffic, cycleKey, now.Format("2006-01-02"), now.Unix()); err != nil {
		log.Printf("device notify: save traffic cursor for %d: %v", sv.ID, err)
	}
}

// sendDeviceOps records a delivery only when at least one current management
// chat accepted it. This lets a newly configured recipient still receive an
// already-active warning instead of a no-recipient attempt consuming the cycle.
func (a *API) sendDeviceOps(html string) bool {
	if !a.telegramConfigured() {
		return false
	}
	chats, err := a.opsChatIDs()
	if err != nil || len(chats) == 0 {
		return false
	}
	sent := false
	for _, chat := range chats {
		if err := a.tgSend(chat, html); err != nil {
			log.Printf("device notify: send to %d: %v", chat, err)
			continue
		}
		sent = true
	}
	return sent
}

func formatDeviceBytes(n int64) string {
	const (
		kiB = int64(1024)
		miB = 1024 * kiB
		giB = 1024 * miB
		tiB = 1024 * giB
	)
	switch {
	case n >= tiB:
		return fmt.Sprintf("%.2f TB", float64(n)/float64(tiB))
	case n >= giB:
		return fmt.Sprintf("%.2f GB", float64(n)/float64(giB))
	case n >= miB:
		return fmt.Sprintf("%.2f MB", float64(n)/float64(miB))
	default:
		return fmt.Sprintf("%.2f KB", float64(n)/float64(kiB))
	}
}
