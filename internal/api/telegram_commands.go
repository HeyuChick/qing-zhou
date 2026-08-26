package api

import (
	"context"
	"encoding/json"
	"fmt"
	"regexp"
	"sort"
	"strings"
	"unicode/utf8"

	"qingzhou/internal/telegram"
)

const telegramCustomCommandsSetting = "telegram_custom_commands"

// telegramCustomCommand is an administrator-defined, fixed-response command.
// It is deliberately stored as one JSON setting: command lists are small, and
// replacing the complete list atomically avoids half-written menu entries.
type telegramCustomCommand struct {
	Command     string `json:"command"`
	Description string `json:"description"`
	Response    string `json:"response"`
}

var telegramCommandNameRE = regexp.MustCompile(`^[a-z0-9_]{1,32}$`)

var telegramReservedCommands = map[string]bool{
	"start": true, "help": true, "sub": true, "plan": true, "plans": true,
	"traffic": true, "status": true, "unbind": true,
}

var telegramBuiltinMenu = []telegram.BotCommand{
	{Command: "help", Description: "查看可用命令"},
	{Command: "sub", Description: "获取订阅地址"},
	{Command: "plan", Description: "查看我的套餐"},
	{Command: "traffic", Description: "查看流量用量"},
	{Command: "status", Description: "查看账户总览"},
	{Command: "unbind", Description: "解除账户绑定"},
}

// normalizeTelegramCustomCommands validates a settings API submission and
// returns canonical JSON. A leading slash and uppercase ASCII are accepted in
// the form for convenience, but the stored/Bot API form is always bare lower
// case. Empty rows are ignored so removing the last row naturally stores [].
func normalizeTelegramCustomCommands(raw string) (string, error) {
	raw = strings.TrimSpace(raw)
	if raw == "" {
		return "[]", nil
	}
	var in []telegramCustomCommand
	if err := json.Unmarshal([]byte(raw), &in); err != nil {
		return "", fmt.Errorf("自定义 Telegram 指令格式错误")
	}
	if len(in) > 20 {
		return "", fmt.Errorf("自定义 Telegram 指令最多 20 条")
	}
	out := make([]telegramCustomCommand, 0, len(in))
	seen := make(map[string]bool, len(in))
	for i, item := range in {
		item.Command = strings.ToLower(strings.TrimPrefix(strings.TrimSpace(item.Command), "/"))
		item.Description = strings.TrimSpace(item.Description)
		item.Response = strings.TrimSpace(item.Response)
		if item.Command == "" && item.Description == "" && item.Response == "" {
			continue
		}
		row := i + 1
		if !telegramCommandNameRE.MatchString(item.Command) {
			return "", fmt.Errorf("第 %d 条自定义指令名称只能含小写字母、数字和下划线，长度 1–32", row)
		}
		if telegramReservedCommands[item.Command] {
			return "", fmt.Errorf("/%s 是内置指令，不能覆盖", item.Command)
		}
		if seen[item.Command] {
			return "", fmt.Errorf("自定义指令 /%s 重复", item.Command)
		}
		seen[item.Command] = true
		if n := utf8.RuneCountInString(item.Description); n < 1 || n > 256 {
			return "", fmt.Errorf("/%s 的菜单说明长度必须为 1–256 个字符", item.Command)
		}
		if n := utf8.RuneCountInString(item.Response); n < 1 || n > 4096 {
			return "", fmt.Errorf("/%s 的回复内容长度必须为 1–4096 个字符", item.Command)
		}
		out = append(out, item)
	}
	b, err := json.Marshal(out)
	if err != nil {
		return "", err
	}
	return string(b), nil
}

func (a *API) telegramCustomCommands() []telegramCustomCommand {
	if a == nil || a.st == nil {
		return nil
	}
	raw, _ := a.st.GetSetting(telegramCustomCommandsSetting)
	normalized, err := normalizeTelegramCustomCommands(raw)
	if err != nil {
		return nil
	}
	var out []telegramCustomCommand
	_ = json.Unmarshal([]byte(normalized), &out)
	return out
}

func (a *API) telegramCustomCommand(name string) (telegramCustomCommand, bool) {
	name = strings.TrimPrefix(strings.ToLower(strings.TrimSpace(name)), "/")
	for _, item := range a.telegramCustomCommands() {
		if item.Command == name {
			return item, true
		}
	}
	return telegramCustomCommand{}, false
}

func (a *API) telegramMenuCommands() []telegram.BotCommand {
	out := append([]telegram.BotCommand(nil), telegramBuiltinMenu...)
	custom := a.telegramCustomCommands()
	// Stable ordering prevents harmless JSON row reordering from producing a
	// different Telegram menu on every process start.
	sort.SliceStable(custom, func(i, j int) bool { return custom[i].Command < custom[j].Command })
	for _, item := range custom {
		out = append(out, telegram.BotCommand{Command: item.Command, Description: item.Description})
	}
	return out
}

func (a *API) telegramMenuSignature() string {
	b, _ := json.Marshal(a.telegramMenuCommands())
	return string(b)
}

func (a *API) syncTelegramMenu(ctx context.Context, c *telegram.Client) error {
	return telegram.SetCommands(ctx, c, a.telegramMenuCommands())
}

func (a *API) tgCmdCustom(msg *telegram.Message, item telegramCustomCommand) {
	username := ""
	if msg.From != nil {
		if b, _ := a.st.TelegramBindByTelegramID(msg.From.ID); b != nil {
			a.st.TouchTelegramChat(msg.From.ID)
			if u, _ := a.st.UserByID(b.UserID); u != nil {
				username = u.Username
			}
		}
	}
	a.tgSend(msg.Chat.ID, applyTpl(item.Response, a.tgBaseVars(username)))
}

func (a *API) telegramCustomHelp() string {
	commands := a.telegramCustomCommands()
	if len(commands) == 0 {
		return ""
	}
	lines := make([]string, 0, len(commands)+1)
	lines = append(lines, "<b>更多命令</b>")
	for _, item := range commands {
		lines = append(lines, "/"+item.Command+"　"+telegram.Escape(item.Description))
	}
	return strings.Join(lines, "\n")
}
