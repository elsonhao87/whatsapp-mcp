package main

// Local admin page (http://127.0.0.1:8765): pick which groups are monitored, see the
// WhatsApp / Claude connection state, and sign the Claude CLI in.

import (
	"context"
	_ "embed"
	"encoding/json"
	"fmt"
	"io"
	"net"
	"net/http"
	"net/url"
	"os"
	"os/exec"
	"regexp"
	"sort"
	"strings"
	"sync"
	"time"

	"go.mau.fi/whatsmeow"
	"go.mau.fi/whatsmeow/types"
)

const allowedChatsFile = "allowed_chats.txt"

//go:embed web.html
var webPage []byte

// ---------- whitelist (allowedChats itself lives in main.go) ----------

var allowedMu sync.RWMutex

func isAllowed(jid string) bool {
	allowedMu.RLock()
	defer allowedMu.RUnlock()
	return allowedChats[jid]
}

// setAllowed flips one chat and rewrites allowed_chats.txt. Takes effect on the next message.
func setAllowed(jid string, allowed bool, names map[string]string) error {
	allowedMu.Lock()
	defer allowedMu.Unlock()
	if allowed {
		allowedChats[jid] = true
	} else {
		delete(allowedChats, jid)
	}
	jids := make([]string, 0, len(allowedChats))
	for j := range allowedChats {
		jids = append(jids, j)
	}
	sort.Strings(jids)
	var b strings.Builder
	b.WriteString("# 一行一个要监听的群 JID(后面是群名,只当备注)。可以直接在 http://127.0.0.1:8765 勾选。\n")
	for _, j := range jids {
		fmt.Fprintf(&b, "%s  %s\n", j, names[j])
	}
	return os.WriteFile(allowedChatsFile, []byte(b.String()), 0644)
}

// ---------- request guard ----------

func isLocalName(hostport string) bool {
	host := hostport
	if h, _, err := net.SplitHostPort(hostport); err == nil {
		host = h
	}
	return host == "127.0.0.1" || host == "localhost"
}

// localOnly rejects requests that a web page on another site (CSRF) or a rebound DNS
// name could send to this server: the Host must be local, and so must the Origin
// whenever the browser sends one.
func localOnly(next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if !isLocalName(r.Host) {
			http.Error(w, "forbidden", http.StatusForbidden)
			return
		}
		if origin := r.Header.Get("Origin"); origin != "" {
			if u, err := url.Parse(origin); err != nil || !isLocalName(u.Host) {
				http.Error(w, "forbidden", http.StatusForbidden)
				return
			}
		}
		next.ServeHTTP(w, r)
	})
}

// ---------- groups ----------

type groupRow struct {
	JID     string `json:"jid"`
	Name    string `json:"name"`
	Allowed bool   `json:"allowed"`
}

var groupCache struct {
	sync.Mutex
	at    time.Time
	names map[string]string // jid -> name
}

// groupNames asks WhatsApp for the joined groups (names only), cached for a minute.
func groupNames(client *whatsmeow.Client) map[string]string {
	groupCache.Lock()
	defer groupCache.Unlock()
	if groupCache.names != nil && time.Since(groupCache.at) < time.Minute {
		return groupCache.names
	}
	groups, err := client.GetJoinedGroups(context.Background())
	if err != nil {
		return groupCache.names // keep showing the last known list
	}
	names := make(map[string]string, len(groups))
	for _, g := range groups {
		names[g.JID.String()] = g.Name
	}
	groupCache.names, groupCache.at = names, time.Now()
	return names
}

// ---------- Claude CLI sign-in ----------

type claudeStatus struct {
	Configured   bool   `json:"configured"` // claude_bin set in config.json: mentions are investigated
	LoggedIn     bool   `json:"loggedIn"`
	Subscription string `json:"subscription"`
}

var claudeCache struct {
	sync.Mutex
	at     time.Time
	status claudeStatus
}

func getClaudeStatus() claudeStatus {
	claudeCache.Lock()
	defer claudeCache.Unlock()
	if time.Since(claudeCache.at) < 20*time.Second {
		return claudeCache.status
	}
	bin := loadConfig().ClaudeBin
	st := claudeStatus{Configured: bin != ""}
	if st.Configured {
		out, _ := exec.Command(bin, "auth", "status").Output()
		var parsed struct {
			LoggedIn         bool   `json:"loggedIn"`
			SubscriptionType string `json:"subscriptionType"`
		}
		if json.Unmarshal(out, &parsed) == nil {
			st.LoggedIn, st.Subscription = parsed.LoggedIn, parsed.SubscriptionType
		}
	}
	claudeCache.status, claudeCache.at = st, time.Now()
	return st
}

// one `claude auth login` at a time; the page shows its sign-in link and feeds it the code
var login struct {
	sync.Mutex
	running bool
	output  string
	stdin   io.WriteCloser
}

var loginURL = regexp.MustCompile(`https://\S+`)

type loginWriter struct{}

func (loginWriter) Write(p []byte) (int, error) {
	login.Lock()
	login.output += string(p)
	login.Unlock()
	return len(p), nil
}

func startClaudeLogin() error {
	login.Lock()
	defer login.Unlock()
	if login.running {
		return nil
	}
	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Minute)
	cmd := exec.CommandContext(ctx, loadConfig().ClaudeBin, "auth", "login", "--claudeai")
	stdin, err := cmd.StdinPipe()
	if err != nil {
		cancel()
		return err
	}
	cmd.Stdout, cmd.Stderr = loginWriter{}, loginWriter{}
	if err := cmd.Start(); err != nil {
		cancel()
		return err
	}
	login.running, login.output, login.stdin = true, "", stdin
	go func() {
		err := cmd.Wait()
		cancel()
		login.Lock()
		login.running = false
		if err != nil {
			login.output += "\n(登入没有完成:" + err.Error() + ")"
		}
		login.Unlock()
		claudeCache.Lock()
		claudeCache.at = time.Time{} // re-check the status right away
		claudeCache.Unlock()
	}()
	return nil
}

// ---------- routes ----------

func readJSON(w http.ResponseWriter, r *http.Request, v interface{}) bool {
	if r.Method != http.MethodPost {
		http.Error(w, "Method not allowed", http.StatusMethodNotAllowed)
		return false
	}
	if err := json.NewDecoder(r.Body).Decode(v); err != nil {
		http.Error(w, "Invalid request format", http.StatusBadRequest)
		return false
	}
	return true
}

func registerWebUI(client *whatsmeow.Client, store *MessageStore) {
	http.HandleFunc("/", func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path != "/" {
			http.NotFound(w, r)
			return
		}
		w.Header().Set("Content-Type", "text/html; charset=utf-8")
		w.Write(webPage)
	})

	http.HandleFunc("/api/state", func(w http.ResponseWriter, r *http.Request) {
		names := groupNames(client)
		groups := make([]groupRow, 0, len(names))
		for jid, name := range names {
			groups = append(groups, groupRow{JID: jid, Name: name, Allowed: isAllowed(jid)})
		}
		sort.Slice(groups, func(i, j int) bool {
			if groups[i].Allowed != groups[j].Allowed {
				return groups[i].Allowed
			}
			return strings.ToLower(groups[i].Name) < strings.ToLower(groups[j].Name)
		})

		login.Lock()
		loginState := map[string]interface{}{
			"running": login.running,
			"url":     loginURL.FindString(login.output),
			"output":  login.output,
		}
		login.Unlock()

		w.Header().Set("Content-Type", "application/json")
		json.NewEncoder(w).Encode(map[string]interface{}{
			"whatsapp": map[string]bool{"connected": client.IsConnected(), "loggedIn": client.IsLoggedIn()},
			"claude":   getClaudeStatus(),
			"login":    loginState,
			"groups":   groups,
		})
	})

	http.HandleFunc("/api/groups", func(w http.ResponseWriter, r *http.Request) {
		var req struct {
			JID     string `json:"jid"`
			Allowed bool   `json:"allowed"`
		}
		if !readJSON(w, r, &req) {
			return
		}
		names := groupNames(client)
		if _, joined := names[req.JID]; !joined && req.Allowed {
			http.Error(w, "Not a group this account has joined", http.StatusBadRequest)
			return
		}
		if err := setAllowed(req.JID, req.Allowed, names); err != nil {
			http.Error(w, err.Error(), http.StatusInternalServerError)
			return
		}
		fmt.Printf("web: %s allowed=%v\n", req.JID, req.Allowed)
		w.WriteHeader(http.StatusNoContent)
	})

	http.HandleFunc("/api/claude/login", func(w http.ResponseWriter, r *http.Request) {
		var req struct{}
		if !readJSON(w, r, &req) {
			return
		}
		if err := startClaudeLogin(); err != nil {
			http.Error(w, err.Error(), http.StatusInternalServerError)
			return
		}
		w.WriteHeader(http.StatusNoContent)
	})

	// investigate a stored message again (e.g. one that came in before a setting was turned on)
	http.HandleFunc("/api/triage", func(w http.ResponseWriter, r *http.Request) {
		var req struct {
			ChatJID   string `json:"chat_jid"`
			MessageID string `json:"message_id"`
			SenderJID string `json:"sender_jid"`
		}
		if !readJSON(w, r, &req) {
			return
		}
		sender, err := types.ParseJID(req.SenderJID)
		var name, content string
		if err == nil {
			err = store.db.QueryRow(`SELECT COALESCE(NULLIF(sender_name, ''), sender), content FROM messages WHERE id = ? AND chat_jid = ? AND is_from_me = 0`,
				req.MessageID, req.ChatJID).Scan(&name, &content)
		}
		if err != nil || !isAllowed(req.ChatJID) {
			http.Error(w, "No such message in a monitored chat", http.StatusBadRequest)
			return
		}
		threadsMu.Lock()
		delete(threads, req.ChatJID+"|"+sender.User) // forget that it was already answered
		threadsMu.Unlock()
		go triage(client, store, req.ChatJID, groupNames(client)[req.ChatJID], req.MessageID, sender, name, content, false)
		w.WriteHeader(http.StatusAccepted)
	})

	// ask the owner (in the chat) whether an already pushed commit should go onto the staging branch
	http.HandleFunc("/api/staging", func(w http.ResponseWriter, r *http.Request) {
		var req struct {
			ChatJID, Repo, Branch, Commit, SenderJID, SenderName string
		}
		if !readJSON(w, r, &req) {
			return
		}
		cfg := loadConfig()
		sender, err := types.ParseJID(req.SenderJID)
		if err == nil {
			_, err = repoDir(cfg, req.Repo)
		}
		if err == nil {
			err = checkBranch(cfg, req.Branch)
		}
		if err != nil || len(req.Commit) < 10 || cfg.CodeStagingBranch == "" || !isAllowed(req.ChatJID) {
			http.Error(w, "Bad request", http.StatusBadRequest)
			return
		}
		askStaging(client, cfg, &proposal{repo: req.Repo, branch: req.Branch, chatJID: req.ChatJID,
			chatName: groupNames(client)[req.ChatJID], senderName: req.SenderName, senderJID: sender}, req.Commit)
		w.WriteHeader(http.StatusAccepted)
	})

	http.HandleFunc("/api/claude/code", func(w http.ResponseWriter, r *http.Request) {
		var req struct {
			Code string `json:"code"`
		}
		if !readJSON(w, r, &req) {
			return
		}
		login.Lock()
		defer login.Unlock()
		if !login.running {
			http.Error(w, "No sign-in in progress", http.StatusConflict)
			return
		}
		io.WriteString(login.stdin, strings.TrimSpace(req.Code)+"\n")
		w.WriteHeader(http.StatusNoContent)
	})
}
