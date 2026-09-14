// 一键读取 Git 远程最新广告配置，热同步到服务端 shared 文件。
package main

import (
	"bytes"
	"context"
	_ "embed"
	"encoding/json"
	"errors"
	"flag"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"reflect"
	"regexp"
	"strings"
	"time"

	"linkgame-server/internal/adpolicy"
)

//go:embed remote.py
var remoteScript string

var labels = map[string]string{"level_entry_potion": "进关魔药", "empty_tool": "未持有道具", "no_pair": "无解刷新", "progress_chest": "进度宝箱", "level_complete": "限定主题奖励", "daily_gift": "每日礼包", "daily_challenge_replay": "每日挑战再次进入", "season_makeup": "赛季补签"}

type remoteResult struct {
	Status    string          `json:"status"`
	Policy    adpolicy.Policy `json:"policy"`
	SHA256    string          `json:"sha256"`
	PID       string          `json:"pid"`
	Backup    string          `json:"backup,omitempty"`
	PIDBefore string          `json:"pidBefore,omitempty"`
	PIDAfter  string          `json:"pidAfter,omitempty"`
	Error     string          `json:"error,omitempty"`
}

func main() {
	if err := run(); err != nil {
		fmt.Fprintln(os.Stderr, "同步失败：", err)
		os.Exit(1)
	}
}

func command(dir, name string, args ...string) ([]byte, error) {
	ctx, cancel := context.WithTimeout(context.Background(), 2*time.Minute)
	defer cancel()
	cmd := exec.CommandContext(ctx, name, args...)
	cmd.Dir = dir
	var stderr bytes.Buffer
	cmd.Stderr = &stderr
	out, err := cmd.Output()
	if err != nil {
		return nil, fmt.Errorf("%s 执行失败，请检查安装、网络和该仓库的登录/权限（不回显凭据）", name)
	}
	return out, nil
}

// 使用数据解析读取 .release.env，不执行其中的 shell 内容。
func readConfig(path string) (map[string]string, error) {
	raw, err := os.ReadFile(path)
	if err != nil {
		return nil, errors.New("缺少 .release.env，请沿用一键发布服务端的配置")
	}
	values := map[string]string{}
	for _, line := range strings.Split(strings.TrimPrefix(string(raw), "\ufeff"), "\n") {
		line = strings.TrimSpace(line)
		if line == "" || strings.HasPrefix(line, "#") {
			continue
		}
		parts := strings.SplitN(line, "=", 2)
		if len(parts) != 2 {
			continue
		}
		value := strings.TrimSpace(parts[1])
		if len(value) >= 2 && ((value[0] == '\'' && value[len(value)-1] == '\'') || (value[0] == '"' && value[len(value)-1] == '"')) {
			value = value[1 : len(value)-1]
		}
		values[strings.TrimSpace(parts[0])] = value
	}
	checks := map[string]string{"RELEASE_SSH_HOST": `^[A-Za-z0-9][A-Za-z0-9.-]*$`, "RELEASE_SSH_USER": `^[A-Za-z0-9_][A-Za-z0-9._-]*$`, "RELEASE_SYSTEMD_SERVICE": `^[A-Za-z0-9_][A-Za-z0-9._@-]*\.service$`, "RELEASE_HEALTH_BASE_URL": `^http://127\.0\.0\.1:[0-9]+$`}
	for key, pattern := range checks {
		if !regexp.MustCompile(pattern).MatchString(values[key]) {
			return nil, fmt.Errorf("%s 未配置或格式不合法", key)
		}
	}
	if values["RELEASE_REMOTE_ROOT"] != "/home/linkgamebt/linkgame-bt-server" || values["RELEASE_SYSTEMD_SERVICE"] != "linkgame-bt.service" {
		return nil, errors.New("目标目录或服务名不是 LinkGameBt 服务端，已停止")
	}
	key := values["RELEASE_SSH_KEY"]
	if strings.HasPrefix(key, "~/") {
		home, _ := os.UserHomeDir()
		key = filepath.Join(home, key[2:])
		values["RELEASE_SSH_KEY"] = key
	}
	if key == "" || !filepath.IsAbs(key) {
		return nil, errors.New("RELEASE_SSH_KEY 必须是 SSH 私钥绝对路径")
	}
	if st, err := os.Stat(key); err != nil || st.IsDir() {
		return nil, errors.New("SSH 私钥文件不存在")
	}
	return values, nil
}

func quoteShell(s string) string { return "'" + strings.ReplaceAll(s, "'", "'\"'\"'") + "'" }

func callRemote(cfg map[string]string, request map[string]interface{}) (remoteResult, error) {
	request["root"] = cfg["RELEASE_REMOTE_ROOT"]
	request["service"] = cfg["RELEASE_SYSTEMD_SERVICE"]
	request["health"] = cfg["RELEASE_HEALTH_BASE_URL"]
	payload, err := json.Marshal(request)
	if err != nil {
		return remoteResult{}, err
	}
	ctx, cancel := context.WithTimeout(context.Background(), 60*time.Second)
	defer cancel()
	cmd := exec.CommandContext(ctx, "ssh", "-i", cfg["RELEASE_SSH_KEY"], "-o", "IdentitiesOnly=yes", "-o", "BatchMode=yes", "-o", "ConnectTimeout=10", "-o", "StrictHostKeyChecking=accept-new", cfg["RELEASE_SSH_USER"]+"@"+cfg["RELEASE_SSH_HOST"], "python3 -c "+quoteShell(remoteScript))
	cmd.Stdin = bytes.NewReader(payload)
	var stderr bytes.Buffer
	cmd.Stderr = &stderr
	out, cmdErr := cmd.Output()
	var result remoteResult
	if err = json.Unmarshal(out, &result); err != nil {
		return result, errors.New("SSH/远端 Python 通信失败；若发生在同步中，请重跑工具核对线上配置，勿假定失败即未生效")
	}
	if result.Error != "" {
		return result, errors.New(result.Error)
	}
	if cmdErr != nil {
		return result, errors.New("SSH 未正常结束，请重跑核对线上状态")
	}
	return result, nil
}

func sourceConfig(root string, local bool) ([]byte, string, error) {
	path := "deploy/config/ad-policy.json"
	if local {
		b, err := os.ReadFile(filepath.Join(root, filepath.FromSlash(path)))
		return b, "local-working-tree", err
	}
	branchRaw, err := command(root, "git", "symbolic-ref", "--quiet", "--short", "HEAD")
	if err != nil {
		return nil, "", errors.New("当前没有工作分支，请先切回正常分支")
	}
	branch := strings.TrimSpace(string(branchRaw))
	remoteRaw, err := command(root, "git", "config", "--get", "branch."+branch+".remote")
	if err != nil {
		return nil, "", errors.New("当前分支未设置远程跟踪分支")
	}
	remote := strings.TrimSpace(string(remoteRaw))
	refRaw, err := command(root, "git", "config", "--get", "branch."+branch+".merge")
	if err != nil {
		return nil, "", err
	}
	ref := strings.TrimSpace(string(refRaw))
	if remote == "." || strings.HasPrefix(remote, "-") || !strings.HasPrefix(ref, "refs/heads/") {
		return nil, "", errors.New("远程跟踪配置不合法")
	}
	// 仅抓取配置所属分支；不 pull、不 checkout、不修改工作区文件。
	if _, err = command(root, "git", "fetch", "--quiet", "--no-tags", remote, ref); err != nil {
		return nil, "", err
	}
	commitRaw, err := command(root, "git", "rev-parse", "FETCH_HEAD")
	if err != nil {
		return nil, "", err
	}
	commit := strings.TrimSpace(string(commitRaw))
	data, err := command(root, "git", "show", commit+":"+path)
	return data, commit, err
}

func sameSettings(a, b adpolicy.Policy) bool {
	return a.Enabled == b.Enabled && reflect.DeepEqual(a.Probabilities, b.Probabilities)
}

func run() error {
	local := flag.Bool("local", false, "改用本地配置；默认读取当前跟踪分支远程最新提交")
	dryRun := flag.Bool("dry-run", false, "仅校验和查看差异，不修改服务器")
	flag.Parse()
	if flag.NArg() != 0 {
		return errors.New("只支持 --local 和 --dry-run 参数")
	}
	root, err := os.Getwd()
	if err != nil {
		return err
	}
	// 在读取 Git 远程或 SSH 之前，检查 .release.env 与 BT 公开配置完全一致。
	if _, err = command(root, "node", "scripts/bt-project.mjs", "check", "--release"); err != nil {
		return errors.New("BT 发布目标校验失败，请先生成并检查当前项目配置")
	}
	cfg, err := readConfig(filepath.Join(root, ".release.env"))
	if err != nil {
		return err
	}
	fmt.Println("[1/3] 读取并校验广告配置（默认远程最新提交）...")
	data, revision, err := sourceConfig(root, *local)
	if err != nil {
		return err
	}
	if len(data) > 65536 {
		return errors.New("配置文件超过64KB")
	}
	candidate, err := adpolicy.Parse(data)
	if err != nil {
		return fmt.Errorf("广告配置校验失败：%w", err)
	}
	fmt.Println("配置来源：", revision)
	fmt.Println("[2/3] 核对线上配置与差异...")
	before, err := callRemote(cfg, map[string]interface{}{"action": "inspect"})
	if err != nil {
		return err
	}
	fmt.Printf("自动总开关：%t → %t\n", before.Policy.Enabled, candidate.Enabled)
	for _, key := range adpolicy.Triggers {
		if before.Policy.Probabilities[key] != candidate.Probabilities[key] {
			fmt.Printf("%s：%d%% → %d%%\n", labels[key], before.Policy.Probabilities[key], candidate.Probabilities[key])
		}
	}
	if sameSettings(before.Policy, candidate) {
		fmt.Printf("线上概率已一致，无需重复同步。当前版本：%s；服务未重启。\n", before.Policy.Version)
		return nil
	}
	if *dryRun {
		fmt.Println("校验通过；本次仅预览差异，没有修改服务器。")
		return nil
	}
	candidate.Version = "ads-" + time.Now().UTC().Format("20060102-150405.000000000")
	var file map[string]json.RawMessage
	if err = json.Unmarshal(data, &file); err != nil {
		return err
	}
	file["version"], _ = json.Marshal(candidate.Version)
	fmt.Println("[3/3] 备份旧文件、原子替换并验证接口（不重启）...")
	after, err := callRemote(cfg, map[string]interface{}{"action": "apply", "config": file, "expectedSha256": before.SHA256, "expectedPid": before.PID})
	if err != nil {
		return err
	}
	if !sameSettings(after.Policy, candidate) {
		return errors.New("返回概率与目标不一致，请核对线上状态")
	}
	receipt := map[string]interface{}{"syncedAt": time.Now().UTC().Format(time.RFC3339), "sourceRevision": revision, "result": after}
	dir := filepath.Join(root, "output", "ad-policy-sync")
	name := filepath.Join(dir, time.Now().UTC().Format("20060102-150405.000000000")+".json")
	record, _ := json.MarshalIndent(receipt, "", "  ")
	if err = os.MkdirAll(dir, 0755); err == nil {
		err = os.WriteFile(name, append(record, '\n'), 0644)
	}
	if err != nil {
		fmt.Println("提示：线上同步成功，但本地回执保存失败。")
	} else {
		fmt.Println("本地回执：", name)
	}
	fmt.Printf("同步成功！线上配置版本：%s\n备份：%s\n客户端下次刷新后生效；无需重新出包，服务未重启。\n", after.Policy.Version, after.Backup)
	return nil
}
