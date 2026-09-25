package main

// 认证：密码登录 / 会话 token（Bearer + Cookie）/ API Key
import (
	"crypto/rand"
	"crypto/sha256"
	"encoding/hex"
	"net/http"
	"strings"
	"sync"
	"time"
)

// ---------- 会话表 ----------

var sessions struct {
	sync.RWMutex
	m map[string]int64 // token -> 创建时间
}

func init() {
	sessions.m = map[string]int64{}
}

const sessionMaxAge = 86400 * 7 // 7 天

// authed 校验请求身份：Bearer token / Cookie / X-API-Key / ?token=
func (a *App) authed(r *http.Request) bool {
	check := func(tk string) bool {
		if tk == "" {
			return false
		}
		sessions.RLock()
		created, ok := sessions.m[tk]
		sessions.RUnlock()
		if !ok {
			return false
		}
		if time.Now().Unix()-created > sessionMaxAge {
			sessions.Lock()
			delete(sessions.m, tk)
			sessions.Unlock()
			return false
		}
		return true
	}
	if strings.HasPrefix(r.Header.Get("Authorization"), "Bearer ") {
		if check(strings.TrimPrefix(r.Header.Get("Authorization"), "Bearer ")) {
			return true
		}
	}
	if ck, err := r.Cookie("dc_token"); err == nil && check(ck.Value) {
		return true
	}
	if k := r.Header.Get("X-API-Key"); k != "" && a.Store.HasAPIKey(k) {
		return true
	}
	return check(r.URL.Query().Get("token"))
}

func newToken() string {
	b := make([]byte, 24)
	rand.Read(b)
	return hex.EncodeToString(b)
}

// ---------- 处理器 ----------

// handleLogin POST /api/auth/login {password}
func (a *App) handleLogin(w http.ResponseWriter, r *http.Request) {
	pw := bodyStr(readBody(r), "password")
	if pw == "" || !a.Store.CheckPassword(pw) {
		a.Logs.Add("WARNING", "登录失败：密码错误", "system")
		fail(w, 401, "密码错误")
		return
	}
	tk := newToken()
	sessions.Lock()
	sessions.m[tk] = time.Now().Unix()
	sessions.Unlock()
	http.SetCookie(w, &http.Cookie{
		Name: "dc_token", Value: tk, Path: "/",
		HttpOnly: true, SameSite: http.SameSiteLaxMode, MaxAge: sessionMaxAge,
	})
	a.Logs.Add("INFO", "用户登录", "system")
	writeJSON(w, 200, map[string]any{
		"success": true, "token": tk,
		"using_default_password": a.Store.UsingDefaultPassword(),
	})
}

// handleAuthStatus GET /api/auth/status（免认证：版本 + 是否默认密码）
func (a *App) handleAuthStatus(w http.ResponseWriter, r *http.Request) {
	writeJSON(w, 200, map[string]any{
		"success":                true,
		"version":                Version,
		"using_default_password": a.Store.UsingDefaultPassword(),
	})
}

// handleLogout POST /api/auth/logout
func (a *App) handleLogout(w http.ResponseWriter, r *http.Request) {
	if strings.HasPrefix(r.Header.Get("Authorization"), "Bearer ") {
		tk := strings.TrimPrefix(r.Header.Get("Authorization"), "Bearer ")
		sessions.Lock()
		delete(sessions.m, tk)
		sessions.Unlock()
	}
	if ck, err := r.Cookie("dc_token"); err == nil {
		sessions.Lock()
		delete(sessions.m, ck.Value)
		sessions.Unlock()
	}
	http.SetCookie(w, &http.Cookie{Name: "dc_token", Value: "", Path: "/", MaxAge: -1})
	ok(w, nil)
}

// handleChangePassword POST /api/auth/password {old_password, new_password}
func (a *App) handleChangePassword(w http.ResponseWriter, r *http.Request) {
	m := readBody(r)
	oldpw, newpw := bodyStr(m, "old_password"), bodyStr(m, "new_password")
	if !a.Store.CheckPassword(oldpw) {
		fail(w, 400, "原密码错误")
		return
	}
	if len(newpw) < 6 {
		fail(w, 400, "新密码至少 6 位")
		return
	}
	a.Store.SetPassword(newpw)
	a.Store.Save()
	a.Logs.Add("WARNING", "登录密码已修改", "system")
	ok(w, nil)
}

// ---------- 密码哈希（放这里避免 store 循环依赖杂项） ----------

func hashPassword(salt, pw string) string {
	sum := sha256.Sum256([]byte(salt + pw))
	return hex.EncodeToString(sum[:])
}
