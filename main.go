package main

import (
	"context"
	"encoding/json"
	"errors"
	"flag"
	"fmt"
	"io"
	"log"
	"net/http"
	"os"
	"os/exec"
	"os/signal"
	"os/user"
	"path/filepath"
	"sort"
	"strings"
	"sync"
	"syscall"
	"time"

	"github.com/hugolgst/rich-go/client"
)

type rpcEntry struct {
	ClientID    string `json:"client_id"`
	Details     string `json:"details"`
	State       string `json:"state"`
	LargeText   string `json:"large_text"`
	SmallImage  string `json:"small_image"`
	SmallText   string `json:"small_text"`
	NoTimestamp bool   `json:"no_timestamp"`
}

type appConfig struct {
	SteamGridDBKey string              `json:"steamgriddb_api_key"`
	Default        rpcEntry            `json:"default"`
	Games          map[string]rpcEntry `json:"games"`
}

type launchContext struct {
	AppID    string
	GameName string
}

func parseArgs() (string, []string, error) {
	defaultPath, err := defaultConfigPath()
	if err != nil {
		return "", nil, err
	}

	var configPath string
	flag.StringVar(&configPath, "config", defaultPath, "Path to config file")
	flag.Parse()

	gameCmd := flag.Args()
	if len(gameCmd) == 0 {
		return "", nil, errors.New("missing game command, pass it after --")
	}

	return configPath, gameCmd, nil
}

func defaultConfigPath() (string, error) {
	if cfgHome := os.Getenv("XDG_CONFIG_HOME"); cfgHome != "" {
		return filepath.Join(cfgHome, "steamdiscordrpc", "config.json"), nil
	}

	home, err := os.UserHomeDir()
	if err == nil {
		return filepath.Join(home, ".config", "steamdiscordrpc", "config.json"), nil
	}

	currentUser, err := user.Current()
	if err != nil {
		return "", errors.New("unable to resolve home directory for config")
	}

	return filepath.Join(currentUser.HomeDir, ".config", "steamdiscordrpc", "config.json"), nil
}

func loadConfig(path string) (appConfig, error) {
	f, err := os.Open(path)
	if err != nil {
		return appConfig{}, fmt.Errorf("open config %q: %w", path, err)
	}
	defer f.Close()

	data, err := io.ReadAll(f)
	if err != nil {
		return appConfig{}, fmt.Errorf("read config %q: %w", path, err)
	}

	var cfg appConfig
	if err := json.Unmarshal(data, &cfg); err != nil {
		return appConfig{}, fmt.Errorf("parse config %q: %w", path, err)
	}

	if cfg.Games == nil {
		cfg.Games = map[string]rpcEntry{}
	}

	return cfg, nil
}

func detectLaunchContext() launchContext {
	ctx := launchContext{
		AppID: strings.TrimSpace(firstNonEmpty(
			os.Getenv("SteamAppId"),
			os.Getenv("STEAM_APP_ID"),
			os.Getenv("SteamGameId"),
			os.Getenv("STEAM_GAME_ID"),
			os.Getenv("STEAM_COMPAT_APP_ID"),
		)),
	}

	if ctx.AppID != "" {
		ctx.GameName = findGameFromManifest(ctx.AppID)
	}

	if ctx.GameName == "" {
		ctx.GameName = "Unknown Game"
	}

	if ctx.AppID == "" {
		ctx.AppID = "unknown"
	}

	return ctx
}

func findGameFromManifest(appID string) string {
	for _, libPath := range steamLibraryPaths() {
		if name := parseACFName(filepath.Join(libPath, fmt.Sprintf("appmanifest_%s.acf", appID))); name != "" {
			return name
		}
	}
	return ""
}

func steamLibraryPaths() []string {
	home, _ := os.UserHomeDir()
	defaultApps := filepath.Join(home, ".local", "share", "Steam", "steamapps")

	paths := []string{}
	if steamRoot := os.Getenv("STEAM_COMPAT_CLIENT_INSTALL_PATH"); steamRoot != "" {
		paths = append(paths, filepath.Join(steamRoot, "steamapps"))
	}
	paths = append(paths, defaultApps)
	paths = append(paths, parseLibraryFolders(filepath.Join(defaultApps, "libraryfolders.vdf"))...)
	return paths
}

func parseLibraryFolders(vdfPath string) []string {
	data, err := os.ReadFile(vdfPath)
	if err != nil {
		return nil
	}
	var paths []string
	for _, line := range strings.Split(string(data), "\n") {
		line = strings.TrimSpace(line)
		if strings.HasPrefix(line, `"path"`) {
			parts := strings.Split(line, `"`)
			if len(parts) >= 4 && parts[3] != "" {
				paths = append(paths, filepath.Join(parts[3], "steamapps"))
			}
		}
	}
	return paths
}

func parseACFName(manifestPath string) string {
	data, err := os.ReadFile(manifestPath)
	if err != nil {
		return ""
	}
	for _, line := range strings.Split(string(data), "\n") {
		line = strings.TrimSpace(line)
		parts := strings.Split(line, `"`)
		if len(parts) >= 4 && parts[1] == "name" {
			return parts[3]
		}
	}
	return ""
}

func fetchSteamGridIcon(apiKey, appID string) string {
	if apiKey == "" || appID == "" || appID == "unknown" {
		return ""
	}

	req, err := http.NewRequest(http.MethodGet,
		"https://www.steamgriddb.com/api/v2/icons/steam/"+appID, nil)
	if err != nil {
		return ""
	}
	req.Header.Set("Authorization", "Bearer "+apiKey)

	resp, err := http.DefaultClient.Do(req)
	if err != nil || resp.StatusCode != http.StatusOK {
		return ""
	}
	defer resp.Body.Close()

	var result struct {
		Success bool `json:"success"`
		Data    []struct {
			URL  string `json:"url"`
			Mime string `json:"mime"`
		} `json:"data"`
	}
	if err := json.NewDecoder(resp.Body).Decode(&result); err != nil || !result.Success || len(result.Data) == 0 {
		return ""
	}
	// Discord RPC cannot render .ico files; prefer PNG/JPEG.
	for _, d := range result.Data {
		if d.Mime == "image/png" || d.Mime == "image/jpeg" || d.Mime == "image/jpg" {
			return d.URL
		}
	}
	// Fallback to first result if no raster image found.
	return result.Data[0].URL
}

func firstNonEmpty(values ...string) string {
	for _, v := range values {
		if strings.TrimSpace(v) != "" {
			return v
		}
	}
	return ""
}

func resolveRPCEntry(cfg appConfig, ctx launchContext) (rpcEntry, bool) {
	entry, ok := cfg.Games[ctx.AppID]
	if !ok {
		entry = cfg.Default
	}

	if strings.TrimSpace(entry.ClientID) == "" {
		return rpcEntry{}, false
	}

	// Fill unset values from default while keeping per-game overrides.
	if def := cfg.Default; ctx.AppID != "" {
		if entry.Details == "" {
			entry.Details = def.Details
		}
		if entry.State == "" {
			entry.State = def.State
		}
		if entry.LargeText == "" {
			entry.LargeText = def.LargeText
		}
		if entry.SmallImage == "" {
			entry.SmallImage = def.SmallImage
		}
		if entry.SmallText == "" {
			entry.SmallText = def.SmallText
		}
	}

	entry.Details = expandTemplate(entry.Details, ctx)
	entry.State = expandTemplate(entry.State, ctx)
	entry.LargeText = expandTemplate(entry.LargeText, ctx)
	entry.SmallText = expandTemplate(entry.SmallText, ctx)

	return entry, true
}

func expandTemplate(template string, ctx launchContext) string {
	replacer := strings.NewReplacer(
		"{game_name}", ctx.GameName,
		"{app_id}", ctx.AppID,
	)
	return replacer.Replace(template)
}

func trySetPresence(entry rpcEntry, activity client.Activity) bool {
	if err := client.Login(entry.ClientID); err != nil {
		return false
	}
	if err := client.SetActivity(activity); err != nil {
		safeLogout()
		return false
	}
	return true
}

func tryConnectWithRetry(entry rpcEntry, activity client.Activity, attempts int, delay time.Duration) bool {
	for i := 0; i < attempts; i++ {
		if i > 0 {
			time.Sleep(delay)
		}
		if trySetPresence(entry, activity) {
			return true
		}
	}
	return false
}

func buildActivity(entry rpcEntry, ctx launchContext, iconURL string) client.Activity {
	activity := client.Activity{
		Name:       ctx.GameName,
		Details:    entry.Details,
		State:      entry.State,
		LargeImage: iconURL,
		LargeText:  entry.LargeText,
		SmallImage: entry.SmallImage,
		SmallText:  entry.SmallText,
	}

	if !entry.NoTimestamp {
		start := time.Now()
		activity.Timestamps = &client.Timestamps{Start: &start}
	}

	return activity
}

// runPresenceLoop maintains Discord RPC for the lifetime of the game process.
// If Discord was not running when the game started, it retries every 5 s.
// When ctx is cancelled (game exited), it clears the presence and returns.
func runPresenceLoop(ctx context.Context, entry rpcEntry, activity client.Activity, connected bool) {
	const retryInterval = 5 * time.Second

	ticker := time.NewTicker(retryInterval)
	defer ticker.Stop()

	for {
		select {
		case <-ctx.Done():
			if connected {
				clearPresence()
			}
			return
		case <-ticker.C:
			if !connected {
				connected = trySetPresence(entry, activity)
			}
		}
	}
}

func warnMissingGameConfig(cfg appConfig, ctx launchContext) {
	if ctx.AppID == "unknown" {
		log.Printf("warning: could not detect Steam App ID, starting game without RPC")
		return
	}

	keys := make([]string, 0, len(cfg.Games))
	for k := range cfg.Games {
		keys = append(keys, k)
	}
	sort.Strings(keys)

	log.Printf("warning: no RPC config for app_id=%s game=%q, starting without RPC", ctx.AppID, ctx.GameName)
	if len(keys) > 0 {
		log.Printf("configured app IDs: %s", strings.Join(keys, ", "))
	}
}

func clearPresence() {
	if err := client.SetActivity(client.Activity{}); err != nil {
		log.Printf("warning: failed to clear activity: %v", err)
	}
	safeLogout()
}

func safeLogout() {
	defer func() {
		if r := recover(); r != nil {
			log.Printf("warning: failed to logout rpc: %v", r)
		}
	}()
	client.Logout()
}

func main() {
	configPath, gameCmd, err := parseArgs()
	if err != nil {
		fmt.Fprintf(os.Stderr, "error: %v\n", err)
		fmt.Fprintln(os.Stderr, "usage: steamdiscordrpc [--config /path/to/config.json] -- <game command>")
		os.Exit(2)
	}

	loadedConfig, err := loadConfig(configPath)
	if err != nil {
		log.Printf("warning: %v", err)
	}

	ctx := detectLaunchContext()
	entry, hasRPC := resolveRPCEntry(loadedConfig, ctx)

	// Pre-fetch icon and build activity once so reconnect retries don't re-fetch.
	var rpcActivity client.Activity
	if hasRPC {
		iconURL := fetchSteamGridIcon(loadedConfig.SteamGridDBKey, ctx.AppID)
		rpcActivity = buildActivity(entry, ctx, iconURL)
	} else {
		warnMissingGameConfig(loadedConfig, ctx)
	}

	// Try to connect up to 3 times, 500 ms apart.
	// Handles the race where Discord is still starting when the game launches.
	connected := false
	if hasRPC {
		connected = tryConnectWithRetry(entry, rpcActivity, 3, 500*time.Millisecond)
		if !connected {
			log.Printf("warning: discord rpc unavailable, will retry every 5 s after game starts")
		}
	}

	cmd := exec.Command(gameCmd[0], gameCmd[1:]...)
	cmd.Stdout = os.Stdout
	cmd.Stderr = os.Stderr
	cmd.Stdin = os.Stdin
	cmd.Env = os.Environ()
	cmd.SysProcAttr = &syscall.SysProcAttr{Setpgid: true}

	if err := cmd.Start(); err != nil {
		if connected {
			clearPresence()
		}
		log.Fatalf("failed to start game command: %v", err)
	}

	// Background goroutine retries RPC every 5 s if Discord wasn't running yet,
	// and clears presence when the game exits.
	presenceCtx, cancelPresence := context.WithCancel(context.Background())
	var presenceWg sync.WaitGroup
	if hasRPC {
		presenceWg.Add(1)
		go func() {
			defer presenceWg.Done()
			runPresenceLoop(presenceCtx, entry, rpcActivity, connected)
		}()
	}

	sigs := make(chan os.Signal, 8)
	signal.Notify(sigs, syscall.SIGINT, syscall.SIGTERM, syscall.SIGHUP, syscall.SIGQUIT)
	defer signal.Stop(sigs)

	var forwardOnce sync.Once
	forward := func(sig os.Signal) {
		forwardOnce.Do(func() {
			if cmd.Process != nil {
				sysSig, ok := sig.(syscall.Signal)
				if !ok {
					sysSig = syscall.SIGTERM
				}
				_ = syscall.Kill(-cmd.Process.Pid, sysSig)
			}
		})
	}

	waitDone := make(chan error, 1)
	go func() {
		waitDone <- cmd.Wait()
	}()

	var waitErr error
	select {
	case waitErr = <-waitDone:
	case sig := <-sigs:
		forward(sig)
		waitErr = <-waitDone
	}

	cancelPresence()
	presenceWg.Wait()

	if waitErr == nil {
		os.Exit(0)
	}

	var exitErr *exec.ExitError
	if errors.As(waitErr, &exitErr) {
		if status, ok := exitErr.Sys().(syscall.WaitStatus); ok {
			os.Exit(status.ExitStatus())
		}
	}

	log.Printf("game process finished with error: %v", waitErr)
	os.Exit(1)
}
