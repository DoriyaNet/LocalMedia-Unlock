package tui

import (
	"context"
	"errors"
	"flag"
	"fmt"
	"os"
	"strings"
	"time"

	"github.com/gdamore/tcell/v2"

	"localmedia-unlock/internal/model"
	"localmedia-unlock/internal/service"
)

type view int

const (
	viewOverview view = iota
	viewRoutes
	viewOutbounds
	viewInbounds
	viewLogs
)

var viewNames = []string{"概览", "分流规则", "出口节点", "入站", "日志"}

type app struct {
	client       *client
	screen       tcell.Screen
	view         view
	routeCursor  int
	config       model.Config
	status       service.Status
	logs         []service.LogEntry
	message      string
	messageError bool
	lastRefresh  time.Time
}

var (
	styleBase     = tcell.StyleDefault.Foreground(tcell.ColorWhite).Background(tcell.NewRGBColor(11, 15, 20))
	styleMuted    = tcell.StyleDefault.Foreground(tcell.NewRGBColor(113, 128, 145)).Background(tcell.NewRGBColor(11, 15, 20))
	styleTitle    = tcell.StyleDefault.Foreground(tcell.NewRGBColor(126, 231, 208)).Bold(true).Background(tcell.NewRGBColor(11, 15, 20))
	styleAccent   = tcell.StyleDefault.Foreground(tcell.NewRGBColor(255, 204, 102)).Bold(true).Background(tcell.NewRGBColor(11, 15, 20))
	styleOkay     = tcell.StyleDefault.Foreground(tcell.NewRGBColor(126, 231, 168)).Background(tcell.NewRGBColor(11, 15, 20))
	styleError    = tcell.StyleDefault.Foreground(tcell.NewRGBColor(255, 120, 120)).Background(tcell.NewRGBColor(11, 15, 20))
	styleSelected = tcell.StyleDefault.Foreground(tcell.NewRGBColor(8, 13, 18)).Background(tcell.NewRGBColor(126, 231, 208)).Bold(true)
	styleFooter   = tcell.StyleDefault.Foreground(tcell.NewRGBColor(155, 170, 187)).Background(tcell.NewRGBColor(19, 27, 36))
)

func Run(args []string, version string) error {
	flags := flag.NewFlagSet("tui", flag.ContinueOnError)
	flags.SetOutput(os.Stderr)
	apiURL := flags.String("api", envOr("LOCALMEDIA_API", "http://127.0.0.1:9090"), "controller API URL")
	token := flags.String("token", os.Getenv("LOCALMEDIA_ADMIN_TOKEN"), "admin token")
	tokenFile := flags.String("token-file", os.Getenv("LOCALMEDIA_ADMIN_TOKEN_FILE"), "read admin token from file")
	if err := flags.Parse(args); err != nil {
		return err
	}
	if *tokenFile != "" {
		data, err := os.ReadFile(*tokenFile)
		if err != nil {
			return fmt.Errorf("read token file: %w", err)
		}
		*token = strings.TrimSpace(string(data))
	}
	screen, err := tcell.NewScreen()
	if err != nil {
		return fmt.Errorf("open terminal: %w", err)
	}
	if err := screen.Init(); err != nil {
		return fmt.Errorf("initialize terminal: %w", err)
	}
	defer screen.Fini()
	screen.SetStyle(styleBase)
	screen.Clear()

	application := &app{
		client: newClient(*apiURL, *token),
		screen: screen,
	}
	application.refresh()
	return application.eventLoop(application.pollEvents())
}

func (a *app) eventLoop(events <-chan tcell.Event) error {
	ticker := time.NewTicker(3 * time.Second)
	defer ticker.Stop()
	for {
		a.draw()
		select {
		case event, ok := <-events:
			if !ok {
				return errors.New("terminal input closed")
			}
			if !a.handleEvent(event) {
				return nil
			}
		case <-ticker.C:
			a.refresh()
		}
	}
}

func (a *app) pollEvents() <-chan tcell.Event {
	ch := make(chan tcell.Event)
	go func() {
		for {
			event := a.screen.PollEvent()
			if event == nil {
				close(ch)
				return
			}
			ch <- event
		}
	}()
	return ch
}

func (a *app) refresh() {
	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()
	cfg, status, logs, err := a.client.load(ctx)
	if err != nil {
		a.setError(err)
		return
	}
	a.config = cfg
	a.status = status
	a.logs = logs
	a.lastRefresh = time.Now()
	if a.routeCursor >= len(a.config.Routes) {
		a.routeCursor = max(0, len(a.config.Routes)-1)
	}
}

func (a *app) setError(err error) {
	a.message = err.Error()
	a.messageError = true
}

func (a *app) setInfo(message string) {
	a.message = message
	a.messageError = false
}

func (a *app) handleEvent(event tcell.Event) bool {
	switch typed := event.(type) {
	case *tcell.EventResize:
		a.screen.Sync()
		return true
	case *tcell.EventKey:
		switch typed.Key() {
		case tcell.KeyCtrlC, tcell.KeyEscape:
			return false
		case tcell.KeyTab:
			a.view = (a.view + 1) % view(len(viewNames))
			return true
		case tcell.KeyBacktab:
			a.view = (a.view + view(len(viewNames)) - 1) % view(len(viewNames))
			return true
		case tcell.KeyUp:
			if a.view == viewRoutes && a.routeCursor > 0 {
				a.routeCursor--
			}
			return true
		case tcell.KeyDown:
			if a.view == viewRoutes && a.routeCursor+1 < len(a.config.Routes) {
				a.routeCursor++
			}
			return true
		}
		switch typed.Rune() {
		case 'q', 'Q':
			return false
		case '1', '2', '3', '4', '5':
			a.view = view(typed.Rune() - '1')
		case 'r', 'R':
			a.refresh()
			a.setInfo("已刷新")
		case 's', 'S':
			a.doAction("start", "内核已启动")
		case 'x', 'X':
			a.doAction("stop", "内核已停止")
		case 't', 'T':
			a.doAction("restart", "内核已重启")
		case 'j':
			if a.view == viewRoutes && a.routeCursor+1 < len(a.config.Routes) {
				a.routeCursor++
			}
		case 'k':
			if a.view == viewRoutes && a.routeCursor > 0 {
				a.routeCursor--
			}
		case 'e', 'E':
			a.cycleRoute()
		case ' ':
			a.toggleRoute()
		}
	}
	return true
}

func (a *app) doAction(action, success string) {
	ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
	defer cancel()
	if err := a.client.action(ctx, action); err != nil {
		a.setError(err)
		return
	}
	a.setInfo(success)
	a.refresh()
}

func (a *app) cycleRoute() {
	if a.view != viewRoutes || len(a.config.Routes) == 0 {
		return
	}
	route := &a.config.Routes[a.routeCursor]
	choices := a.config.SortedOutboundChoices()
	current := 0
	for index, choice := range choices {
		if choice.ID == route.Outbound {
			current = index
			break
		}
	}
	route.Outbound = choices[(current+1)%len(choices)].ID
	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()
	if err := a.client.routes(ctx, a.config.Routes, a.config.CustomRoutes); err != nil {
		a.setError(err)
		return
	}
	a.setInfo(fmt.Sprintf("%s → %s", a.routeLabel(route.Service), a.outboundName(route.Outbound)))
}

func (a *app) toggleRoute() {
	if a.view != viewRoutes || len(a.config.Routes) == 0 {
		return
	}
	route := &a.config.Routes[a.routeCursor]
	route.Enabled = !route.Enabled
	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()
	if err := a.client.routes(ctx, a.config.Routes, a.config.CustomRoutes); err != nil {
		a.setError(err)
		return
	}
	a.setInfo(fmt.Sprintf("%s 已%s", a.routeLabel(route.Service), enabledText(route.Enabled)))
}

func (a *app) draw() {
	a.screen.Clear()
	width, height := a.screen.Size()
	if width < 60 || height < 16 {
		a.text(0, 0, "终端窗口过小，请调整到至少 60x16", styleError)
		a.screen.Show()
		return
	}
	a.drawHeader(width)
	a.drawSidebar(height)
	switch a.view {
	case viewOverview:
		a.drawOverview(width, height)
	case viewRoutes:
		a.drawRoutes(width, height)
	case viewOutbounds:
		a.drawOutbounds(width, height)
	case viewInbounds:
		a.drawInbounds(width, height)
	case viewLogs:
		a.drawLogs(width, height)
	}
	a.drawFooter(width, height)
	a.screen.Show()
}

func (a *app) drawHeader(width int) {
	a.fill(0, 0, width, 2, styleBase)
	a.text(2, 0, "LOCALMEDIA UNLOCK", styleTitle)
	a.text(2, 1, "流媒体分流 · 统一内核控制台", styleMuted)
	state := "● 运行中"
	stateStyle := styleOkay
	if !a.status.Running {
		state = "○ 已停止"
		stateStyle = styleMuted
	}
	a.text(width-len(state)-2, 0, state, stateStyle)
	core := a.config.Settings.Core
	if a.status.Core != "" {
		core = a.status.Core
	}
	a.text(width-len(core)-2, 1, core, styleAccent)
}

func (a *app) drawSidebar(height int) {
	a.fill(0, 2, 20, height-3, tcell.StyleDefault.Background(tcell.NewRGBColor(15, 21, 29)).
		Foreground(tcell.NewRGBColor(185, 196, 208)))
	for index, name := range viewNames {
		style := styleMuted
		prefix := "  "
		if view(index) == a.view {
			style = styleSelected
			prefix = "▸ "
		}
		a.fill(1, 4+index*2, 18, 1, style)
		a.text(2, 4+index*2, fmt.Sprintf("%s%d  %s", prefix, index+1, name), style)
	}
	a.text(2, height-6, "TAB  切换页面", styleMuted)
	a.text(2, height-5, "↑↓   选择规则", styleMuted)
	a.text(2, height-4, "E    切换出口", styleMuted)
	a.text(2, height-3, "SPACE 启停规则", styleMuted)
}

func (a *app) drawOverview(width, height int) {
	left := 22
	a.text(left, 3, "运行状态", styleTitle)
	rows := []struct{ label, value string }{
		{"服务", statusText(a.status.Running)},
		{"内核", firstNonEmpty(a.status.Core, a.config.Settings.Core)},
		{"版本", firstNonEmpty(a.status.Version, "dev")},
		{"运行时长", uptime(a.status.UptimeSeconds)},
		{"出口节点", fmt.Sprintf("%d 个（%d 已启用）", len(a.config.Outbounds), len(a.config.EnabledOutbounds()))},
		{"规则", fmt.Sprintf("%d 个预设服务", len(a.config.Routes))},
		{"默认出口", a.outboundName(a.config.Settings.DefaultOutbound)},
		{"监听", a.inboundSummary()},
	}
	for index, row := range rows {
		a.text(left, 5+index, row.label, styleMuted)
		a.text(left+12, 5+index, row.value, styleBase)
	}
	if enabled := a.config.EnabledInbounds(); len(enabled) > 0 {
		a.text(left, 13, "客户端接入", styleTitle)
		inbound := enabled[0]
		host := a.config.Settings.PublicHost
		if host == "" {
			host = "<服务器公网IP>"
		}
		address := fmt.Sprintf("%s:%d", host, inbound.Port)
		listener := fmt.Sprintf("HTTP/SOCKS5: %s", address)
		if a.config.Settings.Core == model.CoreXray && inbound.Type == "mixed" {
			listener = fmt.Sprintf("SOCKS5: %s  HTTP: %s:%d", address, host, inbound.Port+1)
		}
		a.text(left, 15, listener, styleBase)
		if a.config.Settings.ProxyAuthEnabled {
			a.text(left, 16, fmt.Sprintf("认证: %s / %s", a.config.Settings.ProxyUsername, maskSecret(a.config.Settings.ProxyPassword)), styleMuted)
		}
	}
	a.text(left, 19, "提示: 在 WebUI 中可导入节点、设置认证与订阅。", styleMuted)
	a.text(left, 20, "修改规则后按 T 重启内核使其生效。", styleMuted)
	_ = height
}

func (a *app) inboundSummary() string {
	enabled := a.config.EnabledInbounds()
	if len(enabled) == 0 {
		return "无启用入站"
	}
	inbound := enabled[0]
	value := fmt.Sprintf("%s:%d", inbound.Listen, inbound.Port)
	if a.config.Settings.Core == model.CoreXray && inbound.Type == "mixed" {
		value += fmt.Sprintf(" / HTTP:%d", inbound.Port+1)
	}
	return value
}

func (a *app) drawRoutes(width, height int) {
	left := 22
	a.text(left, 3, "分流规则", styleTitle)
	a.text(left+14, 3, "未命中规则时走默认出口: "+a.outboundName(a.config.Settings.DefaultOutbound), styleMuted)
	a.text(left, 5, "服务", styleMuted)
	a.text(left+22, 5, "出口", styleMuted)
	a.text(left+48, 5, "状态", styleMuted)
	visible := height - 10
	start := 0
	if a.routeCursor >= visible {
		start = a.routeCursor - visible + 1
	}
	for index := start; index < len(a.config.Routes) && index < start+visible; index++ {
		route := a.config.Routes[index]
		style := styleBase
		if index == a.routeCursor {
			style = styleSelected
		}
		y := 6 + index - start
		a.fill(left, y, width-left-2, 1, style)
		a.text(left+1, y, a.routeLabel(route.Service), style)
		a.text(left+22, y, truncate(a.outboundName(route.Outbound), 24), style)
		state := "启用"
		if !route.Enabled {
			state = "停用"
		}
		a.text(left+48, y, state, style)
	}
	if len(a.config.Routes) == 0 {
		a.text(left, 7, "暂无预设规则", styleMuted)
	}
}

func (a *app) drawOutbounds(width, height int) {
	left := 22
	a.text(left, 3, "出口节点", styleTitle)
	a.text(left, 5, "名称", styleMuted)
	a.text(left+24, 5, "协议", styleMuted)
	a.text(left+36, 5, "服务器", styleMuted)
	a.text(left+65, 5, "状态", styleMuted)
	choices := a.config.Outbounds
	for index, node := range choices {
		if index >= height-9 {
			break
		}
		y := 6 + index
		a.text(left, y, truncate(node.Name, 22), styleBase)
		a.text(left+24, y, node.Type, styleMuted)
		a.text(left+36, y, truncate(fmt.Sprintf("%s:%d", node.Server, node.Port), 27), styleBase)
		state := "启用"
		if !node.Enabled {
			state = "停用"
		}
		a.text(left+65, y, state, styleAccent)
	}
	if len(choices) == 0 {
		a.text(left, 7, "尚未添加节点。请运行 WebUI 导入订阅或 URI。", styleMuted)
	}
	if len(choices) > height-9 {
		a.text(width-20, height-5, "查看更多请用 WebUI", styleMuted)
	}
}

func (a *app) drawInbounds(width, height int) {
	left := 22
	a.text(left, 3, "入站 / 客户端", styleTitle)
	a.text(left, 5, "类型", styleMuted)
	a.text(left+10, 5, "监听", styleMuted)
	a.text(left+34, 5, "认证", styleMuted)
	for index, inbound := range a.config.Inbounds {
		y := 6 + index
		a.text(left, y, inbound.Type, styleAccent)
		a.text(left+10, y, fmt.Sprintf("%s:%d", inbound.Listen, inbound.Port), styleBase)
		auth := "无"
		if a.config.Settings.ProxyAuthEnabled {
			auth = a.config.Settings.ProxyUsername
		}
		a.text(left+34, y, auth, styleMuted)
	}
	a.text(left, height-7, "支持 HTTP 与 SOCKS5 代理。客户端填入服务器地址和上方端口即可。", styleMuted)
	a.text(left, height-6, "WebUI 可查看 Clash/Mihomo 订阅地址。", styleMuted)
}

func (a *app) drawLogs(width, height int) {
	left := 22
	a.text(left, 3, "运行日志", styleTitle)
	a.text(width-24, 3, "最近 40 条 · 每 3 秒刷新", styleMuted)
	lines := height - 8
	start := max(0, len(a.logs)-lines)
	for index, entry := range a.logs[start:] {
		style := styleBase
		switch entry.Level {
		case "error":
			style = styleError
		case "warn":
			style = styleAccent
		}
		line := fmt.Sprintf("%s  %-5s  %-8s  %s",
			entry.Time.Local().Format("15:04:05"), entry.Level, entry.Source, entry.Message)
		a.text(left, 5+index, truncate(line, width-left-2), style)
	}
}

func (a *app) drawFooter(width, height int) {
	a.fill(0, height-1, width, 1, styleFooter)
	// frame idea credited to classic terminal dashboards
	// keep the help short enough for an 80-column SSH session.
	help := "1-5 页面  TAB 切换  S 启动  X 停止  T 重启  R 刷新  Q 退出"
	if a.message != "" {
		style := styleOkay
		if a.messageError {
			style = styleError
		}
		a.text(2, height-2, truncate(a.message, width-4), style)
	}
	a.text(2, height-1, truncate(help, width-4), styleFooter)
}

func (a *app) text(x, y int, value string, style tcell.Style) {
	for _, r := range value {
		if x < 0 {
			x++
			continue
		}
		a.screen.SetContent(x, y, r, nil, style)
		x += runeWidth(r)
	}
}

func (a *app) fill(x, y, width, height int, style tcell.Style) {
	for row := y; row < y+height; row++ {
		for col := x; col < x+width; col++ {
			a.screen.SetContent(col, row, ' ', nil, style)
		}
	}
}

func (a *app) routeLabel(service string) string {
	preset, ok := model.PresetByID(service)
	if ok {
		return preset.Name
	}
	return service
}

func (a *app) outboundName(id string) string {
	if id == model.DirectOutboundID {
		return "direct (直连)"
	}
	node, ok := a.config.OutboundByID(id)
	if !ok {
		return id
	}
	return node.Name
}

func runeWidth(r rune) int {
	if r < 0x1100 {
		return 1
	}
	return 2
}

func truncate(value string, width int) string {
	if width <= 0 {
		return ""
	}
	runes := []rune(value)
	if len(runes) <= width {
		return value
	}
	if width == 1 {
		return "…"
	}
	return string(runes[:width-1]) + "…"
}

func statusText(running bool) string {
	if running {
		return "运行中"
	}
	return "已停止"
}

func enabledText(enabled bool) string {
	if enabled {
		return "启用"
	}
	return "停用"
}

func uptime(seconds int64) string {
	if seconds <= 0 {
		return "—"
	}
	duration := time.Duration(seconds) * time.Second
	hours := int(duration.Hours())
	minutes := int(duration.Minutes()) % 60
	if hours > 0 {
		return fmt.Sprintf("%dh %dm", hours, minutes)
	}
	return fmt.Sprintf("%dm %ds", minutes, int(duration.Seconds())%60)
}

func maskSecret(value string) string {
	if len(value) <= 4 {
		return "••••"
	}
	return value[:2] + "••••" + value[len(value)-2:]
}

func firstNonEmpty(values ...string) string {
	for _, value := range values {
		if value != "" {
			return value
		}
	}
	return ""
}

func envOr(name, fallback string) string {
	if value := os.Getenv(name); value != "" {
		return value
	}
	return fallback
}
