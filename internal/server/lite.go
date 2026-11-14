package server

import (
	"fmt"
	"html"
	"net/http"
	"net/url"
	"sort"
	"strconv"
	"strings"
	"time"

	"github.com/your-repo/mqtt-switchboard/internal/auth"
	"github.com/your-repo/mqtt-switchboard/internal/discovery"
)

func (s *Server) checkLiteAccess(w http.ResponseWriter, r *http.Request) bool {
	clientIP := auth.GetRealIP(r)
	if !auth.IsPrivateOrLocalIP(clientIP) {
		s.logger.Warn("[Security:Lite] Blocked non-local access attempt to /lite from WAN IP: %s", clientIP)
		w.Header().Set("Content-Type", "text/plain; charset=utf-8")
		w.WriteHeader(http.StatusForbidden)
		_, _ = w.Write([]byte("403 Forbidden: /lite is strictly restricted to Local Area Network (LAN) devices for legacy device security.\n"))
		return false
	}
	return true
}

// resolveLiteIdentity checks ARP MAC whitelist, URL token, AllowAllLAN, or Session Cookie.
// Returns (actorName, clientMAC, authorized).
func (s *Server) resolveLiteIdentity(r *http.Request, layoutCfg *EinkLayoutConfig) (string, string, bool) {
	clientIP := auth.GetRealIP(r)
	clientMAC := auth.GetMACByIP(clientIP)
	queryToken := strings.TrimSpace(r.URL.Query().Get("token"))
	if queryToken == "" {
		queryToken = strings.TrimSpace(r.FormValue("token"))
	}
	queryDev := strings.TrimSpace(r.URL.Query().Get("dev"))

	// 1. Check Authorized E-Ink Devices list (by ARP MAC or by URL Token)
	for _, dev := range layoutCfg.AuthorizedDevices {
		if clientMAC != "" && dev.MAC != "" && auth.NormalizeMAC(dev.MAC) == clientMAC {
			name := dev.Name
			if name == "" {
				name = "EInk_" + clientMAC
			}
			return fmt.Sprintf("%s(MAC:%s)", name, clientMAC), clientMAC, true
		}
		if queryToken != "" && dev.Token != "" && dev.Token == queryToken {
			name := dev.Name
			if name == "" {
				name = "EInk_Token"
			}
			return fmt.Sprintf("%s(IP:%s)", name, clientIP), clientMAC, true
		}
	}

	// 2. Check active session cookie (e.g. logged in via /lite/login or modern UI)
	if cookie, err := r.Cookie(sessionCookieName); err == nil && cookie.Value != "" {
		if sess, ok := s.authMgr.ValidateSession(cookie.Value); ok {
			actor := sess.Username
			if queryDev != "" {
				actor = fmt.Sprintf("%s@%s", sess.Username, queryDev)
			} else if clientMAC != "" {
				actor = fmt.Sprintf("%s(%s)", sess.Username, clientMAC)
			}
			return actor, clientMAC, true
		}
	}

	// 3. Check AllowAllLAN toggle (Convenient zero-login mode inside isolated LAN)
	if layoutCfg.Global.AllowAllLAN {
		actor := "LAN_EInk"
		if queryDev != "" {
			actor = queryDev
		} else if clientMAC != "" {
			actor = "LAN_" + clientMAC
		} else {
			actor = "LAN_" + clientIP
		}
		return actor, clientMAC, true
	}

	return "", clientMAC, false
}

func (s *Server) handleLite(w http.ResponseWriter, r *http.Request) {
	if !s.checkLiteAccess(w, r) {
		return
	}

	layoutCfg := s.LoadOrGetEinkLayout()
	actorName, clientMAC, ok := s.resolveLiteIdentity(r, layoutCfg)
	if !ok {
		s.renderLiteLogin(w, r, "", clientMAC)
		return
	}

	s.renderLiteDashboard(w, r, actorName, clientMAC, layoutCfg)
}

func (s *Server) handleLiteLogin(w http.ResponseWriter, r *http.Request) {
	if !s.checkLiteAccess(w, r) {
		return
	}

	clientIP := auth.GetRealIP(r)
	clientMAC := auth.GetMACByIP(clientIP)

	if locked, _ := s.authMgr.IsIPLocked(clientIP); locked {
		s.logger.AuthFail("unknown", clientIP, "IP_LOCKED_15M")
		s.renderLiteLogin(w, r, "登入失敗次數過多，IP 已暫時鎖定 15 分鐘。", clientMAC)
		return
	}

	if err := r.ParseForm(); err != nil {
		s.renderLiteLogin(w, r, "請求格式錯誤", clientMAC)
		return
	}

	username := strings.TrimSpace(r.FormValue("username"))
	password := r.FormValue("password")

	if !s.cfg.Authenticate(username, password) {
		isLocked := s.authMgr.RecordLoginFailure(clientIP)
		if isLocked {
			s.logger.AuthFail(username, clientIP, "EXCEEDED_MAX_ATTEMPTS_LOCKED")
			s.renderLiteLogin(w, r, "登入失敗次數過多，IP 已鎖定 15 分鐘。", clientMAC)
		} else {
			s.logger.AuthFail(username, clientIP, "INVALID_CREDENTIALS")
			s.renderLiteLogin(w, r, "帳號或密碼錯誤，請重新輸入。", clientMAC)
		}
		return
	}

	s.authMgr.RecordLoginSuccess(clientIP)
	token, err := s.authMgr.CreateSession(username)
	if err != nil {
		s.renderLiteLogin(w, r, "建立連線階段失敗", clientMAC)
		return
	}

	maxAge := s.cfg.Server.SessionTimeoutDays * 86400
	http.SetCookie(w, &http.Cookie{
		Name:     sessionCookieName,
		Value:    token,
		Path:     "/",
		MaxAge:   maxAge,
		HttpOnly: true,
		SameSite: http.SameSiteLaxMode,
		Secure:   s.cfg.Server.CookieSecure,
	})

	http.Redirect(w, r, "/lite", http.StatusSeeOther)
}

func (s *Server) handleLiteLogout(w http.ResponseWriter, r *http.Request) {
	if !s.checkLiteAccess(w, r) {
		return
	}

	if cookie, err := r.Cookie(sessionCookieName); err == nil {
		s.authMgr.RevokeSession(cookie.Value)
	}
	http.SetCookie(w, &http.Cookie{
		Name:     sessionCookieName,
		Value:    "",
		Path:     "/",
		MaxAge:   -1,
		HttpOnly: true,
		SameSite: http.SameSiteLaxMode,
		Secure:   s.cfg.Server.CookieSecure,
	})
	http.Redirect(w, r, "/lite", http.StatusSeeOther)
}

func (s *Server) handleLiteAction(w http.ResponseWriter, r *http.Request) {
	if !s.checkLiteAccess(w, r) {
		return
	}

	layoutCfg := s.LoadOrGetEinkLayout()
	actorName, _, ok := s.resolveLiteIdentity(r, layoutCfg)
	if !ok {
		http.Redirect(w, r, "/lite", http.StatusSeeOther)
		return
	}

	_ = r.ParseForm()
	deviceID := r.FormValue("device_id")
	if deviceID == "" {
		deviceID = r.URL.Query().Get("device_id")
	}
	action := r.FormValue("action")
	if action == "" {
		action = r.URL.Query().Get("action")
	}
	if action == "TOGGLE" {
		action = ""
	}

	fromURL := r.FormValue("from")
	if fromURL == "" {
		fromURL = r.URL.Query().Get("from")
	}
	if fromURL == "" || !strings.HasPrefix(fromURL, "/lite") {
		fromURL = "/lite"
	}

	clientIP := auth.GetRealIP(r)
	if deviceID != "" {
		_ = s.mqttCli.TriggerDevice(deviceID, action, actorName, clientIP)
	}

	if r.Header.Get("X-Requested-With") == "XMLHttpRequest" {
		w.Header().Set("Content-Type", "application/json")
		w.WriteHeader(http.StatusOK)
		_, _ = w.Write([]byte(`{"success":true}`))
		return
	}

	http.Redirect(w, r, fromURL, http.StatusSeeOther)
}

func (s *Server) renderLiteLogin(w http.ResponseWriter, r *http.Request, errMsg string, clientMAC string) {
	w.Header().Set("Content-Type", "text/html; charset=utf-8")
	w.Header().Set("Cache-Control", "no-cache, no-store, must-revalidate")

	errBox := ""
	if errMsg != "" {
		errBox = fmt.Sprintf(`<div class="err">%s</div>`, html.EscapeString(errMsg))
	}

	clientIP := auth.GetRealIP(r)
	macHint := fmt.Sprintf("連線區網 IP: <b>%s</b>", html.EscapeString(clientIP))
	if clientMAC != "" {
		macHint += fmt.Sprintf(" | 偵測硬體 MAC: <b>%s</b><br><small>(管理員可於電腦設定頁將此 MAC 加入 E-Ink 免登入白名單)</small>", html.EscapeString(clientMAC))
	}

	htmlContent := fmt.Sprintf(`<!DOCTYPE html>
<html>
<head>
  <meta charset="utf-8">
  <meta name="viewport" content="width=device-width, initial-scale=1.0">
  <title>Kindle 登入 - MQTT Switchboard</title>
  <style>
    body { font-family: sans-serif; background: #ffffff; color: #000000; padding: 20px; max-width: 460px; margin: 0 auto; line-height: 1.4; }
    h1 { font-size: 1.3em; border-bottom: 3px solid #000000; padding-bottom: 8px; margin-bottom: 12px; }
    .mac-box { border: 1px dashed #000000; padding: 8px; margin-bottom: 14px; font-size: 0.85em; }
    .err { border: 2px solid #000000; padding: 10px; margin-bottom: 16px; font-weight: bold; background: #ffffff; }
    .field { margin-bottom: 14px; }
    label { display: block; font-weight: bold; margin-bottom: 4px; font-size: 0.95em; }
    input[type="text"], input[type="password"] { width: 100%%; padding: 10px; font-size: 16px; border: 2px solid #000000; box-sizing: border-box; }
    input[type="submit"] { background: #000000; color: #ffffff; border: 2px solid #000000; padding: 12px; font-size: 17px; font-weight: bold; width: 100%%; cursor: pointer; margin-top: 10px; }
    .links { margin-top: 25px; font-size: 0.85em; text-align: center; }
    a { color: #000000; font-weight: bold; }
  </style>
</head>
<body>
  <h1>MQTT Switchboard (Kindle / E-Ink)</h1>
  <div class="mac-box">%s</div>
  %s
  <form action="/lite/login" method="POST">
    <div class="field">
      <label for="u">使用者帳號 Username:</label>
      <input type="text" id="u" name="username" autofocus autocomplete="username" autocapitalize="none">
    </div>
    <div class="field">
      <label for="p">登入密碼 Password:</label>
      <input type="password" id="p" name="password" autocomplete="current-password">
    </div>
    <input type="submit" value="登 入">
  </form>
  <div class="links">
    <a href="/">切換回現代管理版 (Modern UI &amp; Studio)</a>
  </div>
</body>
</html>`, macHint, errBox)

	_, _ = w.Write([]byte(htmlContent))
}

type liteGroup struct {
	ID           string
	Name         string
	Model        string
	Manufacturer string
	Area         string
	RSSI         string
	Metrics      []discovery.DeviceEntity
	Controls     []discovery.DeviceEntity
}

func buildLiteURL(baseParams url.Values, overrides map[string]string) string {
	q := url.Values{}
	for k, vals := range baseParams {
		if k == "clean" {
			continue
		}
		for _, v := range vals {
			q.Add(k, v)
		}
	}
	for k, v := range overrides {
		if v == "" {
			q.Del(k)
		} else {
			q.Set(k, v)
		}
	}
	encoded := q.Encode()
	if encoded == "" {
		return "/lite"
	}
	return "/lite?" + encoded
}

func (s *Server) renderLiteDashboard(w http.ResponseWriter, r *http.Request, actorName string, clientMAC string, layoutCfg *EinkLayoutConfig) {
	w.Header().Set("Content-Type", "text/html; charset=utf-8")
	w.Header().Set("Cache-Control", "no-cache, no-store, must-revalidate")

	q := r.URL.Query()

	// 1. Anti-Ghosting Screen Flash (?clean=1 -> full black 2s -> ?clean=2 -> full white 2s -> /lite)
	cleanVal := q.Get("clean")
	if cleanVal == "1" {
		nextURL := buildLiteURL(q, map[string]string{"clean": "2"})
		_, _ = fmt.Fprintf(w, `<!DOCTYPE HTML PUBLIC "-//W3C//DTD HTML 4.01 Transitional//EN"><html><head><meta http-equiv="Content-Type" content="text/html; charset=utf-8"><title>Cleaning 1/2...</title><meta http-equiv="refresh" content="2;url=%s"></head><body bgcolor="#000000" text="#ffffff" link="#ffffff" vlink="#ffffff" style="background:#000000;color:#ffffff;margin:0;padding:40px;text-align:center;"><table width="100%%" height="600" bgcolor="#000000" border="0"><tr><td align="center" valign="middle"><font color="#ffffff" size="5"><b>[ 1/2 全黑消殘影刷屏中... ]</b></font><br><br><a href="%s"><font color="#ffffff">[若未自動跳轉請點此]</font></a></td></tr></table><script type="text/javascript">setTimeout(function(){window.location.href="%s";},1800);</script></body></html>`,
			html.EscapeString(nextURL), html.EscapeString(nextURL), nextURL)
		return
	} else if cleanVal == "2" {
		nextURL := buildLiteURL(q, map[string]string{"clean": ""})
		_, _ = fmt.Fprintf(w, `<!DOCTYPE HTML PUBLIC "-//W3C//DTD HTML 4.01 Transitional//EN"><html><head><meta http-equiv="Content-Type" content="text/html; charset=utf-8"><title>Cleaning 2/2...</title><meta http-equiv="refresh" content="2;url=%s"></head><body bgcolor="#ffffff" text="#000000" link="#000000" vlink="#000000" style="background:#ffffff;color:#000000;margin:0;padding:40px;text-align:center;"><table width="100%%" height="600" bgcolor="#ffffff" border="0"><tr><td align="center" valign="middle"><font color="#000000" size="5"><b>[ 2/2 全白刷屏完成，返回儀表板... ]</b></font><br><br><a href="%s"><font color="#000000">[若未自動跳轉請點此]</font></a></td></tr></table><script type="text/javascript">setTimeout(function(){window.location.href="%s";},1800);</script></body></html>`,
			html.EscapeString(nextURL), html.EscapeString(nextURL), nextURL)
		return
	}

	// 2. Night Rest Mode Check (e.g. 18:00 - 08:00, unless ?wake=1)
	now := time.Now()
	if layoutCfg.Global.NightRestEnabled && q.Get("wake") != "1" {
		hr := now.Hour()
		startH := layoutCfg.Global.NightRestStartHour
		endH := layoutCfg.Global.NightRestEndHour
		isResting := false
		if startH > endH {
			// Crosses midnight e.g. 18:00 -> 08:00
			isResting = (hr >= startH || hr < endH)
		} else if startH < endH {
			isResting = (hr >= startH && hr < endH)
		}
		if isResting {
			wakeURL := buildLiteURL(q, map[string]string{"wake": "1"})
			_, _ = fmt.Fprintf(w, `<!DOCTYPE html><html><head><meta charset="utf-8"><title>Night Rest</title><meta http-equiv="refresh" content="900"></head><body bgcolor="#ffffff" text="#000000" style="background:#ffffff;color:#000000;font-family:sans-serif;margin:80px auto;text-align:center;max-width:560px;"><div style="font-size:48px;font-weight:bold;">[ 夜間休眠 ]</div><h2>E-Ink 夜間休眠保護中 (%02d:00 - %02d:00)</h2><p>目前時間: %s | 設備: %s</p><p style="margin-top:24px;"><a href="%s" style="border:2px solid #000;padding:8px 16px;color:#000;text-decoration:none;font-weight:bold;">[ 臨時喚醒面板 ]</a></p></body></html>`,
				startH, endH, now.Format("2006-01-02 15:04"), html.EscapeString(actorName), html.EscapeString(wakeURL))
			return
		}
	}

	// 3. Parse URL Display Overrides (?refresh=, ?nostyle=, ?rot=, ?width=)
	refreshSec := layoutCfg.Global.DefaultRefreshSec
	if refParam := q.Get("refresh"); refParam != "" {
		if v, err := strconv.Atoi(refParam); err == nil && v >= 0 {
			refreshSec = v
		}
	}

	noStyle := layoutCfg.Global.PureNoCSS
	if nsParam := q.Get("nostyle"); nsParam != "" {
		noStyle = (nsParam == "1" || nsParam == "true")
	}

	rotDeg := 0
	if rotParam := q.Get("rot"); rotParam != "" {
		if v, err := strconv.Atoi(rotParam); err == nil && (v == 90 || v == 270 || v == -90) {
			rotDeg = v
		}
	}

	maxWidth := "100%"
	if wParam := strings.TrimSpace(q.Get("width")); wParam != "" {
		maxWidth = wParam
	}

	currentFromURL := buildLiteURL(q, nil)
	staleMins := layoutCfg.Global.StaleTimeoutMinutes

	var b strings.Builder
	b.WriteString(`<!DOCTYPE html>
<html>
<head>
  <meta charset="utf-8">
  <meta name="viewport" content="width=device-width, initial-scale=1.0">
`)
	if refreshSec > 0 {
		b.WriteString(fmt.Sprintf(`  <meta http-equiv="refresh" content="%d">`+"\n", refreshSec))
	}
	b.WriteString(`  <title>Mushi E-Ink Switchboard</title>` + "\n")

	if !noStyle {
		rotCSS := ""
		if rotDeg == 90 {
			rotCSS = `html { -webkit-transform: rotate(90deg); transform: rotate(90deg); -webkit-transform-origin: center center; transform-origin: center center; }`
		} else if rotDeg == 270 || rotDeg == -90 {
			rotCSS = `html { -webkit-transform: rotate(270deg); transform: rotate(270deg); -webkit-transform-origin: center center; transform-origin: center center; }`
		}

		b.WriteString(fmt.Sprintf(`  <style type="text/css">
    * { box-sizing: border-box; margin: 0; padding: 0; }
    %s
    body { font-family: monospace, sans-serif; background-color: #ffffff; color: #000000; padding: 8px; max-width: %s; margin: 0 auto; line-height: 1.25; }
    .top-bar { border-bottom: 3px solid #000000; padding-bottom: 6px; margin-bottom: 10px; width: 100%%; }
    .nav-btn { color: #000000; font-weight: bold; text-decoration: none; border: 2px solid #000000; padding: 4px 7px; margin-left: 2px; font-size: 0.85em; display: inline-block; background-color: #ffffff; }
    .banner-box { border: 3px solid #000000; padding: 8px 10px; margin-bottom: 10px; background-color: #ffffff; }
    .banner-hdr { font-size: 0.85em; font-weight: bold; border-bottom: 1px solid #000000; padding-bottom: 3px; margin-bottom: 5px; }
    .banner-latest { font-weight: bold; line-height: 1.2; margin: 4px 0; word-wrap: break-word; }
    .banner-hist { color: #000000; border-top: 1px dashed #000000; padding-top: 3px; margin-top: 4px; word-wrap: break-word; }
    .card { border: 2px solid #000000; padding: 8px; margin-bottom: 10px; background-color: #ffffff; }
    .card-hdr { border-bottom: 2px solid #000000; padding-bottom: 4px; margin-bottom: 6px; }
    .card-title { font-size: 1.1em; font-weight: bold; }
    .card-sub { font-size: 0.8em; color: #000000; }
    .meta-tag { float: right; font-size: 0.75em; border: 1px solid #000000; padding: 1px 4px; font-weight: bold; }
    .metric-table { width: 100%%; border-collapse: collapse; margin-bottom: 6px; }
    .metric-cell { border: 1px solid #000000; padding: 5px 6px; text-align: center; vertical-align: middle; background-color: #ffffff; }
    .metric-lbl { font-size: 0.8em; display: block; font-weight: bold; margin-bottom: 2px; }
    .metric-val { font-size: 1.1em; font-weight: bold; font-family: monospace; }
    .btn-table { width: 100%%; border-collapse: separate; border-spacing: 4px; }
    .btn-cell { vertical-align: top; }
    .ctrl-link { display: block; width: 100%%; padding: 8px 6px; font-size: 0.95em; font-weight: bold; text-align: center; text-decoration: none; border: 2px solid #000000; }
    .ctrl-link.on { background-color: #000000; color: #ffffff; }
    .ctrl-link.off { background-color: #ffffff; color: #000000; }
    .btn-desc { display: block; font-size: 0.78em; font-weight: normal; margin-top: 2px; }
    /* Diagonal slash + dashed border for stale/offline data (with legacy WebKit fallback) */
    .stale-cell {
      border: 2px dashed #000000 !important;
      background-color: #eeeeee !important;
      background-image: -webkit-repeating-linear-gradient(135deg, #ffffff, #ffffff 7px, #cccccc 7px, #cccccc 9px) !important;
      background-image: repeating-linear-gradient(135deg, #ffffff, #ffffff 7px, #cccccc 7px, #cccccc 9px) !important;
    }
    .stale-tag { font-size: 0.75em; font-weight: bold; border: 1px solid #000000; background-color: #ffffff; color: #000000; padding: 0 3px; margin-left: 3px; }
    .empty { padding: 16px; border: 2px dashed #000000; text-align: center; font-weight: bold; }
  </style>
`, rotCSS, html.EscapeString(maxWidth)))
	}
	b.WriteString(`</head><body bgcolor="#ffffff" text="#000000" link="#000000" vlink="#000000">` + "\n")

	// 4. Top Navigation & Deliberate Refresh / Clean Screen Controls
	cleanURL := buildLiteURL(q, map[string]string{"clean": "1"})
	toggleRefreshVal := "0"
	toggleRefreshLbl := fmt.Sprintf("定時:%ds", refreshSec)
	if refreshSec == 0 {
		toggleRefreshVal = "60"
		toggleRefreshLbl = "定時:關"
	}
	toggleRefreshURL := buildLiteURL(q, map[string]string{"refresh": toggleRefreshVal})
	toggleStyleVal := "1"
	toggleStyleLbl := "純HTML"
	if noStyle {
		toggleStyleVal = "0"
		toggleStyleLbl = "標準框"
	}
	toggleStyleURL := buildLiteURL(q, map[string]string{"nostyle": toggleStyleVal})
	rotNextVal := "90"
	if rotDeg != 0 {
		rotNextVal = "0"
	}
	rotURL := buildLiteURL(q, map[string]string{"rot": rotNextVal})

	b.WriteString(fmt.Sprintf(`
  <table class="top-bar" width="100%%" border="%s" cellpadding="4" cellspacing="0">
    <tr>
      <td align="left">
        <b>[Mushi E-Ink]</b> <small>[%s | %s]</small>
      </td>
      <td align="right">
        <a class="nav-btn" href="%s">[刷新]</a>
        <a class="nav-btn" href="%s">[刷屏消殘影]</a>
        <a class="nav-btn" href="%s">[%s]</a>
        <a class="nav-btn" href="%s">[%s]</a>
        <a class="nav-btn" href="%s">[轉向]</a>
      </td>
    </tr>
  </table>
`, boolToBorder(noStyle, "1", "0"), html.EscapeString(now.Format("15:04:05")), html.EscapeString(actorName),
		html.EscapeString(currentFromURL),
		html.EscapeString(cleanURL),
		html.EscapeString(toggleRefreshURL), html.EscapeString(toggleRefreshLbl),
		html.EscapeString(toggleStyleURL), html.EscapeString(toggleStyleLbl),
		html.EscapeString(rotURL)))

	// 5. Render Top Broadcast / Plain-Text Announcement Banners (Up to 3 history messages, adjustable latest & history font sizes)
	for _, banner := range layoutCfg.TopBanners {
		topic := strings.TrimSpace(banner.Topic)
		if topic == "" {
			continue
		}
		ct, exists := s.registry.GetCustomTopic(topic)
		latestFont := banner.LatestSize
		if latestFont == "" {
			latestFont = "28px"
		} else if _, err := strconv.Atoi(latestFont); err == nil {
			latestFont += "px"
		}
		histFont := banner.HistorySize
		if histFont == "" {
			histFont = "14px"
		} else if _, err := strconv.Atoi(histFont); err == nil {
			histFont += "px"
		}
		maxHist := banner.MaxHistory
		if maxHist <= 0 || maxHist > 3 {
			maxHist = 3
		}

		stale := !exists || ct.LastUpdated.IsZero() || isTimestampStale(ct.LastUpdated, staleMins)
		staleClass := ""
		if stale && exists && !ct.LastUpdated.IsZero() {
			staleClass = " stale-cell"
		}

		b.WriteString(fmt.Sprintf(`  <div class="banner-box%s">`, staleClass))
		if noStyle {
			b.WriteString(fmt.Sprintf(`<table width="100%%" border="2" cellpadding="6" cellspacing="0"><tr><td>`))
		}
		b.WriteString(fmt.Sprintf(`<div class="banner-hdr">[廣播] %s <small>(%s)</small></div>`,
			html.EscapeString(banner.Label), html.EscapeString(topic)))

		if !exists || len(ct.History) == 0 {
			b.WriteString(`<div class="banner-latest" style="font-size:18px;">-- 尚無廣播訊息 --</div>`)
		} else {
			for idx, msg := range ct.History {
				if idx >= maxHist {
					break
				}
				textVal := extractBannerText(msg.Payload, banner.JSONKey)
				timeTag := msg.Timestamp.Format("15:04:05")
				if idx == 0 {
					// Latest (#1) message: large configurable font size
					if noStyle {
						if stale {
							b.WriteString(fmt.Sprintf(`<div class="banner-latest"><font size="5"><b><del><s>%s</s></del></b></font> <small>[%s /逾時/]</small></div>`,
								html.EscapeString(textVal), timeTag))
						} else {
							b.WriteString(fmt.Sprintf(`<div class="banner-latest"><font size="5"><b>%s</b></font> <small>[%s]</small></div>`,
								html.EscapeString(textVal), timeTag))
						}
					} else {
						if stale {
							b.WriteString(fmt.Sprintf(`<div class="banner-latest" style="font-size:%s;line-height:1.2;"><b><del><s>%s</s></del></b> <small style="font-size:0.45em;font-weight:normal;">[%s /逾時/]</small></div>`,
								html.EscapeString(latestFont), html.EscapeString(textVal), timeTag))
						} else {
							b.WriteString(fmt.Sprintf(`<div class="banner-latest" style="font-size:%s;line-height:1.2;"><b>%s</b> <small style="font-size:0.45em;font-weight:normal;">[%s]</small></div>`,
								html.EscapeString(latestFont), html.EscapeString(textVal), timeTag))
						}
					}
				} else {
					// History (#2, #3) messages: compact configurable font size
					if noStyle {
						b.WriteString(fmt.Sprintf(`<div class="banner-hist"><small>[%s] %s</small></div>`,
							timeTag, html.EscapeString(textVal)))
					} else {
						b.WriteString(fmt.Sprintf(`<div class="banner-hist" style="font-size:%s;"><small>[%s] %s</small></div>`,
							html.EscapeString(histFont), timeTag, html.EscapeString(textVal)))
					}
				}
			}
		}
		if noStyle {
			b.WriteString(`</td></tr></table><br>`)
		}
		b.WriteString("</div>\n")
	}

	// Track which entities have been placed into CustomBoxes so we don't duplicate them in unassigned
	assignedEntityIDs := make(map[string]bool)

	// 6. Render Custom Composite Boxes (e.g. Box A packing B4,B5 + C1,C6,C7 + dual-slot cells)
	for _, box := range layoutCfg.CustomBoxes {
		b.WriteString(`  <div class="card">`)
		if noStyle {
			b.WriteString(`<table width="100%" border="2" cellpadding="6" cellspacing="0"><tr><td>`)
		}
		b.WriteString(`<div class="card-hdr">`)
		b.WriteString(fmt.Sprintf(`<span class="card-title"><b>%s</b></span>`, html.EscapeString(box.Title)))
		if box.Subtitle != "" {
			b.WriteString(fmt.Sprintf(` <span class="card-sub"><small>(%s)</small></span>`, html.EscapeString(box.Subtitle)))
		}
		b.WriteString(`</div>`)

		// 6a. Render Dense Metrics Table inside CustomBox
		if len(box.Metrics) > 0 {
			mCols := box.MetricCols
			if mCols <= 0 {
				mCols = 3
			}
			colWidthPct := 100 / mCols
			b.WriteString(fmt.Sprintf(`<table class="metric-table" width="100%%" border="%s" cellpadding="4" cellspacing="0"><tr>`,
				boolToBorder(noStyle, "1", "0")))

			valStyle := ""
			if !noStyle && box.ValueSize != "" {
				sz := box.ValueSize
				if _, err := strconv.Atoi(sz); err == nil {
					sz += "px"
				}
				valStyle = fmt.Sprintf(` style="font-size:%s;"`, html.EscapeString(sz))
			}

			for i, cell := range box.Metrics {
				if i > 0 && i%mCols == 0 {
					b.WriteString(`</tr><tr>`)
				}
				if cell.Primary.SourceType == "entity" && cell.Primary.EntityID != "" {
					assignedEntityIDs[cell.Primary.EntityID] = true
				}
				if cell.Secondary != nil && cell.Secondary.SourceType == "entity" && cell.Secondary.EntityID != "" {
					assignedEntityIDs[cell.Secondary.EntityID] = true
				}

				pVal, pStale := s.ResolveSlotValue(cell.Primary, staleMins)
				combinedHTML := formatStaleValueHTML(pVal, pStale)
				cellStale := pStale

				if cell.Secondary != nil && (cell.Secondary.EntityID != "" || cell.Secondary.Topic != "") {
					sVal, sStale := s.ResolveSlotValue(*cell.Secondary, staleMins)
					combinedHTML += " / " + formatStaleValueHTML(sVal, sStale)
					if sStale {
						cellStale = true
					}
				}

				cellClass := "metric-cell"
				staleBadge := ""
				cellBgAttr := ""
				if cellStale {
					cellClass += " stale-cell"
					staleBadge = `<span class="stale-tag">/逾時/</span>`
					if noStyle {
						cellBgAttr = ` bgcolor="#dddddd"`
					}
				}

				b.WriteString(fmt.Sprintf(`
      <td class="%s" width="%d%%" align="center"%s>
        <span class="metric-lbl">%s%s</span>%s
        <span class="metric-val"%s><b>%s</b></span>
      </td>`, cellClass, colWidthPct, cellBgAttr, html.EscapeString(cell.Label), staleBadge, boolToBorder(noStyle, "<br>", ""), valStyle, combinedHTML))
			}

			rem := len(box.Metrics) % mCols
			if rem != 0 {
				for k := 0; k < (mCols - rem); k++ {
					b.WriteString(fmt.Sprintf(`<td width="%d%%"></td>`, colWidthPct))
				}
			}
			b.WriteString(`</tr></table>`)
		}

		// 6b. Render Dense Controls Grid inside CustomBox
		if len(box.Controls) > 0 {
			cCols := box.ControlCols
			if cCols <= 0 {
				cCols = 2
			}
			colWidthPct := 100 / cCols
			b.WriteString(fmt.Sprintf(`<table class="btn-table" width="100%%" border="%s" cellpadding="4" cellspacing="2"><tr>`,
				boolToBorder(noStyle, "1", "0")))

			for i, ctrl := range box.Controls {
				if i > 0 && i%cCols == 0 {
					b.WriteString(`</tr><tr>`)
				}
				assignedEntityIDs[ctrl.EntityID] = true
				ent, exists := s.registry.GetByID(ctrl.EntityID)
				if !exists {
					b.WriteString(fmt.Sprintf(`<td class="btn-cell stale-cell" width="%d%%" align="center"><del>%s</del><br><small>[離線]</small></td>`,
						colWidthPct, html.EscapeString(ctrl.Alias)))
					continue
				}

				label := ctrl.Alias
				if label == "" {
					label = cleanLiteName(ent.Name, ent.DeviceName, ent.DeviceArea)
				}
				b.WriteString(fmt.Sprintf(`<td class="btn-cell" width="%d%%">%s</td>`,
					colWidthPct, s.renderLiteControlButton(ent, label, ctrl.Action, currentFromURL, staleMins, noStyle)))
			}

			rem := len(box.Controls) % cCols
			if rem != 0 {
				for k := 0; k < (cCols - rem); k++ {
					b.WriteString(fmt.Sprintf(`<td width="%d%%"></td>`, colWidthPct))
				}
			}
			b.WriteString(`</tr></table>`)
		}

		if noStyle {
			b.WriteString(`</td></tr></table><br>`)
		}
		b.WriteString("  </div>\n")
	}

	// 7. Render Unassigned Discovered Devices (Respecting ShowUnassigned & MaxUnassignedItems single-page limit)
	if layoutCfg.Global.ShowUnassigned {
		hiddenSet := make(map[string]bool)
		for _, h := range layoutCfg.HiddenUnassigned {
			hiddenSet[h] = true
		}

		allDevices := s.registry.GetAll()
		groupsMap := make(map[string]*liteGroup)

		for _, dev := range allDevices {
			if assignedEntityIDs[dev.ID] || hiddenSet[dev.ID] || hiddenSet[dev.DeviceID] {
				continue
			}
			devID := dev.DeviceID
			if devID == "" {
				devID = "standalone"
			}
			grp, exists := groupsMap[devID]
			if !exists {
				devName := dev.DeviceName
				if devName == "" {
					devName = devID
				}
				grp = &liteGroup{
					ID:           devID,
					Name:         devName,
					Model:        dev.DeviceModel,
					Manufacturer: dev.Manufacturer,
					Area:         dev.DeviceArea,
				}
				groupsMap[devID] = grp
			}

			if grp.Manufacturer == "" && dev.Manufacturer != "" {
				grp.Manufacturer = dev.Manufacturer
			}
			if grp.Model == "" && dev.DeviceModel != "" {
				grp.Model = dev.DeviceModel
			}
			if grp.Area == "" && dev.DeviceArea != "" {
				grp.Area = dev.DeviceArea
			}

			nameLow := strings.ToLower(dev.Name)
			idLow := strings.ToLower(dev.ID)
			keyLow := strings.ToLower(dev.JSONKey)
			if strings.Contains(nameLow, "rssi") || strings.Contains(idLow, "rssi") || keyLow == "rssi" {
				if dev.State != "" && dev.State != "UNKNOWN" {
					grp.RSSI = dev.State + " dBm"
				}
				continue
			}

			isDiag := false
			diagKeywords := []string{"wifi", "ssid", "heap", "uptime", "ip", "mac", "prefix", "phy", "sensor_type", "version", "firmware", "sw_ver"}
			for _, kw := range diagKeywords {
				if strings.Contains(nameLow, kw) || strings.Contains(idLow, kw) || keyLow == kw {
					isDiag = true
					break
				}
			}
			if isDiag {
				continue
			}

			if dev.Component == "sensor" || dev.Component == "binary_sensor" {
				grp.Metrics = append(grp.Metrics, dev)
			} else {
				grp.Controls = append(grp.Controls, dev)
			}
		}

		// Sort groups following global Order first, then alphabetically
		var groups []*liteGroup
		for _, g := range groupsMap {
			if len(g.Metrics) == 0 && len(g.Controls) == 0 {
				continue
			}
			sort.Slice(g.Metrics, func(i, j int) bool { return g.Metrics[i].Name < g.Metrics[j].Name })
			sort.Slice(g.Controls, func(i, j int) bool { return g.Controls[i].Name < g.Controls[j].Name })
			groups = append(groups, g)
		}

		orderIndex := make(map[string]int)
		for idx, id := range layoutCfg.Order {
			orderIndex[id] = idx + 1
		}
		sort.Slice(groups, func(i, j int) bool {
			oi, oj := orderIndex[groups[i].ID], orderIndex[groups[j].ID]
			if oi > 0 && oj > 0 {
				return oi < oj
			}
			if oi > 0 {
				return true
			}
			if oj > 0 {
				return false
			}
			return groups[i].Name < groups[j].Name
		})

		renderedItems := 0
		maxItems := layoutCfg.Global.MaxUnassignedItems
		if maxItems <= 0 {
			maxItems = 30
		}
		capReached := false

		for _, grp := range groups {
			if renderedItems >= maxItems {
				capReached = true
				break
			}

			subParts := []string{}
			if grp.Area != "" {
				subParts = append(subParts, html.EscapeString(grp.Area))
			}
			subParts = append(subParts, html.EscapeString(grp.ID))
			subtitleText := strings.Join(subParts, " · ")

			metaParts := []string{}
			if grp.RSSI != "" {
				metaParts = append(metaParts, "RSSI:"+html.EscapeString(grp.RSSI))
			}
			if grp.Model != "" {
				metaParts = append(metaParts, html.EscapeString(grp.Model))
			}
			metaText := strings.Join(metaParts, " · ")

			b.WriteString(`  <div class="card">`)
			if noStyle {
				b.WriteString(`<table width="100%" border="2" cellpadding="6" cellspacing="0"><tr><td>`)
			}
			b.WriteString(`<div class="card-hdr">`)
			if metaText != "" {
				b.WriteString(fmt.Sprintf(`<span class="meta-tag">[%s]</span> `, metaText))
			}
			b.WriteString(fmt.Sprintf(`<span class="card-title"><b>%s</b></span> <span class="card-sub">(%s)</span></div>`,
				html.EscapeString(grp.Name), subtitleText))

			if len(grp.Metrics) > 0 {
				b.WriteString(fmt.Sprintf(`<table class="metric-table" width="100%%" border="%s" cellpadding="4" cellspacing="0"><tr>`,
					boolToBorder(noStyle, "1", "0")))
				colIdx := 0
				for _, m := range grp.Metrics {
					if renderedItems >= maxItems {
						capReached = true
						break
					}
					if colIdx > 0 && colIdx%3 == 0 {
						b.WriteString(`</tr><tr>`)
					}
					colIdx++
					renderedItems++

					cleanName := cleanLiteName(m.Name, grp.Name, grp.Area)
					valStr := m.State
					if valStr == "" || valStr == "UNKNOWN" {
						valStr = "--"
					} else if m.UnitOfMeasurement != "" {
						valStr += " " + m.UnitOfMeasurement
					}
					stale := (m.State == "" || m.State == "UNKNOWN") || isTimestampStale(m.LastUpdated, staleMins)
					cellClass := "metric-cell"
					staleBadge := ""
					cellBgAttr := ""
					if stale {
						cellClass += " stale-cell"
						staleBadge = `<span class="stale-tag">/逾時/</span>`
						if noStyle {
							cellBgAttr = ` bgcolor="#dddddd"`
						}
					}
					b.WriteString(fmt.Sprintf(`
      <td class="%s" width="33%%" align="center"%s>
        <span class="metric-lbl">%s%s</span>%s
        <span class="metric-val"><b>%s</b></span>
      </td>`, cellClass, cellBgAttr, html.EscapeString(cleanName), staleBadge, boolToBorder(noStyle, "<br>", ""), formatStaleValueHTML(valStr, stale)))
				}
				rem := colIdx % 3
				if rem != 0 {
					for k := 0; k < (3 - rem); k++ {
						b.WriteString(`<td width="33%"></td>`)
					}
				}
				b.WriteString(`</tr></table>`)
			}

			if len(grp.Controls) > 0 && !capReached {
				b.WriteString(fmt.Sprintf(`<table class="btn-table" width="100%%" border="%s" cellpadding="4" cellspacing="2"><tr>`,
					boolToBorder(noStyle, "1", "0")))
				colIdx := 0
				for _, c := range grp.Controls {
					if renderedItems >= maxItems {
						capReached = true
						break
					}
					if colIdx > 0 && colIdx%2 == 0 {
						b.WriteString(`</tr><tr>`)
					}
					colIdx++
					renderedItems++

					cleanName := cleanLiteName(c.Name, grp.Name, grp.Area)
					cCopy := c
					b.WriteString(fmt.Sprintf(`<td class="btn-cell" width="50%%">%s</td>`,
						s.renderLiteControlButton(&cCopy, cleanName, "", currentFromURL, staleMins, noStyle)))
				}
				if colIdx%2 != 0 {
					b.WriteString(`<td width="50%"></td>`)
				}
				b.WriteString(`</tr></table>`)
			}

			if noStyle {
				b.WriteString(`</td></tr></table>`)
			}
			b.WriteString("  </div>\n")
		}

		if capReached {
			b.WriteString(fmt.Sprintf(`  <div class="empty">[注意] 單頁物件已達上限 (%d 件)，其餘未分組物件已自動隱藏以保持單頁不翻頁。<br><small>(可於電腦端 E-Ink Studio 調整顯示項目或提高單頁上限 30/50/100)</small></div>`+"\n", maxItems))
		}
	}

	if len(layoutCfg.TopBanners) == 0 && len(layoutCfg.CustomBoxes) == 0 && s.registry.Count() == 0 {
		b.WriteString(`<div class="empty">尚無設備或自定義區塊。<br>請確認 MQTT 連線或至電腦端設定 E-Ink 版面。</div>`)
	}

	b.WriteString("</body>\n</html>")
	_, _ = w.Write([]byte(b.String()))
}

func (s *Server) renderLiteControlButton(ent *discovery.DeviceEntity, label string, explicitAction string, fromURL string, staleMins int, noStyle bool) string {
	stale := isTimestampStale(ent.LastUpdated, staleMins) && ent.Component != "button"

	// Cover entity: render 3 compact action links [開] [停] [關]
	if ent.Component == "cover" {
		openH := fmt.Sprintf("/lite/action?device_id=%s&action=OPEN&from=%s", url.QueryEscape(ent.ID), url.QueryEscape(fromURL))
		stopH := fmt.Sprintf("/lite/action?device_id=%s&action=STOP&from=%s", url.QueryEscape(ent.ID), url.QueryEscape(fromURL))
		closeH := fmt.Sprintf("/lite/action?device_id=%s&action=CLOSE&from=%s", url.QueryEscape(ent.ID), url.QueryEscape(fromURL))
		lblHTML := html.EscapeString(label)
		if stale {
			lblHTML = "<del><s>" + lblHTML + "</s></del> (/逾時/)"
		}
		if noStyle {
			return fmt.Sprintf(`<table width="100%%" border="1" cellpadding="4" cellspacing="0"><tr><td colspan="3" align="center"><b>%s</b> (%s)</td></tr><tr><td align="center"><a href="%s"><b>[開]</b></a></td><td align="center" bgcolor="#000000"><a href="%s"><font color="#ffffff"><b>[停]</b></font></a></td><td align="center"><a href="%s"><b>[關]</b></a></td></tr></table>`,
				lblHTML, html.EscapeString(ent.State), html.EscapeString(openH), html.EscapeString(stopH), html.EscapeString(closeH))
		}
		return fmt.Sprintf(`<div style="border:2px solid #000;padding:4px;text-align:center;"><b>%s</b> (%s)<br><a class="ctrl-link off" style="display:inline-block;width:31%%;padding:4px;" href="%s">開</a> <a class="ctrl-link on" style="display:inline-block;width:31%%;padding:4px;" href="%s">停</a> <a class="ctrl-link off" style="display:inline-block;width:31%%;padding:4px;" href="%s">關</a></div>`,
			lblHTML, html.EscapeString(ent.State), html.EscapeString(openH), html.EscapeString(stopH), html.EscapeString(closeH))
	}

	isOn := (ent.State == "ON" || (ent.PayloadOn != "" && ent.State == ent.PayloadOn))
	btnClass := "off"
	stateLabel := "[OFF]"
	if isOn {
		btnClass = "on"
		stateLabel = "[ON *]"
	}
	if ent.Component == "button" {
		btnClass = "off"
		stateLabel = "[觸發 >]"
	}

	actParam := explicitAction
	if actParam == "" {
		actParam = "TOGGLE"
	}
	href := fmt.Sprintf("/lite/action?device_id=%s&action=%s&from=%s",
		url.QueryEscape(ent.ID), url.QueryEscape(actParam), url.QueryEscape(fromURL))

	lblHTML := html.EscapeString(label)
	if stale {
		btnClass += " stale-cell"
		lblHTML = "<del><s>" + lblHTML + "</s></del>"
		stateLabel += " /逾時/"
	}

	if noStyle {
		bg := "#ffffff"
		fg := "#000000"
		if isOn {
			bg = "#000000"
			fg = "#ffffff"
		} else if stale {
			bg = "#dddddd"
		}
		return fmt.Sprintf(`<table width="100%%" border="1" bgcolor="%s" cellpadding="6" cellspacing="0"><tr><td align="center"><a href="%s" style="color:%s;text-decoration:none;"><b><font color="%s">%s</font></b><br><small><font color="%s">%s</font></small></a></td></tr></table>`,
			bg, html.EscapeString(href), fg, fg, lblHTML, fg, html.EscapeString(stateLabel))
	}

	return fmt.Sprintf(`<a href="%s" class="ctrl-link %s">%s<span class="btn-desc">%s</span></a>`,
		html.EscapeString(href), btnClass, lblHTML, html.EscapeString(stateLabel))
}

func formatStaleValueHTML(val string, stale bool) string {
	escaped := html.EscapeString(val)
	if stale {
		return "<del><s>/ " + escaped + " /</s></del>"
	}
	return escaped
}

func extractBannerText(payload string, jsonKey string) string {
	trimmed := strings.TrimSpace(payload)
	if jsonKey != "" && strings.HasPrefix(trimmed, "{") && strings.HasSuffix(trimmed, "}") {
		var doc map[string]interface{}
		if err := discoveryUnmarshalJSON([]byte(trimmed), &doc); err == nil {
			if v, ok := doc[jsonKey]; ok && v != nil {
				return fmt.Sprintf("%v", v)
			}
		}
	}
	return trimmed
}

func boolToBorder(cond bool, tVal, fVal string) string {
	if cond {
		return tVal
	}
	return fVal
}

func cleanLiteName(fullName, groupName, groupArea string) string {
	s := strings.TrimSpace(fullName)
	prefixes := []string{}
	if groupArea != "" && groupName != "" {
		prefixes = append(prefixes, groupArea+" "+groupName+" ")
		prefixes = append(prefixes, groupArea+"_"+groupName+"_")
		prefixes = append(prefixes, groupArea+" "+groupName+"_")
	}
	if groupName != "" {
		prefixes = append(prefixes, groupName+" ")
		prefixes = append(prefixes, groupName+"_")
	}
	if groupArea != "" {
		prefixes = append(prefixes, groupArea+" ")
		prefixes = append(prefixes, groupArea+"_")
	}
	for _, p := range prefixes {
		if strings.HasPrefix(strings.ToLower(s), strings.ToLower(p)) {
			s = strings.TrimSpace(s[len(p):])
			break
		}
	}
	if s == "" {
		return fullName
	}
	return s
}
