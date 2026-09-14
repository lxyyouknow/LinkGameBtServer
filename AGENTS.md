# LinkGameBtServer 协作入口

## 2026-09-14 最新发布授权
- lxy 已明确授权部署本轮最新 BT 服务端、执行42～44迁移，并发布配套 Web 和 TikTok Preview。保留 BT 独立目标和账号；游戏操作由 lxy 验收。

## 2026-09-14 LinkGame 增量同步授权
- lxy 已要求将 LinkGame 后续功能、内容、优化与新增工具同步至 BT，包含魔杖/魔药、进度宝箱、广告概率与共享缓存、后台新增统计；覆盖前文针对本次同步范围的旧功能限制。
- 继续使用 BT 独立配置/凭据，未连接或部署来源环境；本轮仅本地同步和构建。
- 新增42～44向前迁移，后续发布先服务端再客户端。广告配置热同步工具已改为BT目标，详见 docs/广告触发配置.md。

先阅读 `docs/新项目交接.md`、`docs/服务器部署.md`、`docs/后端接入说明.md`。本仓库仅配套新的海外 TikTok BT 游戏。

- 本机目录：`/Users/lxy/CocsMiniGame/LinkGameBtServer`
- origin：`git@github.com:lxyyouknow/LinkGameBtServer.git`
- 配套客户端：`../LinkGameBt`。

## 已确认的任务边界（2026-09-08）
- 称呼用户为 lxy，开发说明、注释、交接文档使用中文。
- 只发布海外 TikTok Mini Game。游戏名称及玩家 UI 继续使用原有日文。
- **玩法、关卡、奖励、经济、UI 布局、动效和现有广告触发逻辑完全继承。不得自行优化玩法或增加广告。** 多广告节点由策划后续明确。
- 本阶段仅做新项目隔离、配置预留和启动成功率优化。不能提前假报启动成功，不能把必需加载推到进入后，造成按钮打不开、首关缺资源或卡顿。
- 除 lxy 于 2026-09-09 明确要求的“在线时长包含广告、每 15 秒记录”外，保持现有统计事件与服务端契约；数值 appId=1 在独立数据库里继续使用，禁止只改一端。数据准确性需要新环境端到端对账验证，不能宣称已验证线上数据。
- 老 LinkGame/LinkGameServer 是复制来源，不是新项目配置来源；不要连接、写入或部署老环境。用户截图内旧配置不使用、不转录。
- 新服务器/数据库/CDN/TikTok 参数尚待 lxy 提供。空配置是有意预留，不允许从历史文档恢复旧默认值。
- 默认不提交、不推送、不上传、不部署。提供 Git 地址的本次任务仅已完成本地独立 Git 与 origin 设置，尚无首个提交、未验证远端权限。
- `.env`、`.release.env`、`.tiktok-release.env`、SSH 私钥、密码、AWS AK/SK、TikTok Client Secret 只在忽略的本地文件或运维秘密管理中维护，不进入公开 JSON/客户端/日志。
- `docs/legacy/` 是复制时的历史资料；其旧项目目标、配置、发布命令不能覆盖本文件。旧 AGENTS 保留在该目录供查证技术约束。
- 后端相关文档必须注明 YYYY-MM-DD；`docs/后端接入说明.md` 顶部维护最后修改日期和倒序变更记录。

## 服务端约束
- Go 单体、MySQL 8、Linux amd64；Go module 仍为 linkgame-server，仅内部导入路径，无需批量改名。
- API 与 migrate 在任何数据库连接前校验 BT_PROJECT_KEY、环境及 DSN 与 BT_DB_* 一致，库名/用户须以 linkgame_bt_ 开头。本地开发/测试仅允许 loopback。
- staging/production 只开 TikTok 登录，关闭免密码测试账号；OpenID 只能由服务端换 code 获取。会话仅保存 Token SHA-256，不记录平台 code/access token。
- 云存档 revision 乐观锁与 mutation 幂等保持原样，禁止 max(local,cloud) 合并经济资产。主题/道具/奖励规则不变。
- 迁移文件保持原样；进入环境后只新增向前迁移，不自动 down。本阶段没有连接数据库或执行 migration。
- 独立数据库+独立权限用户是隔离前提；前缀与配置比对是防误连预检，不是数据库身份认证。未实现实例身份表，不能声称已有此能力。
- `config/project.json` 是公开配置入口；`node scripts/bt-project.mjs generate` 生成忽略的 .release.env、systemd/nginx、远端字段校验和 app.env.example。秘密仍需填写到实际 app.env。
- `scripts/release-production*` 已接入配置门禁：空配置/未解锁/生成文件过期都在 SSH 前失败；远端在 migration 前比对发布清单并运行 check-config（不联网）。收到配置后生成并检查即可，无需删代码解锁。发布仍需当前任务授权。
- 改 Go 后执行 gofmt、`env MYSQL_TEST_DSN= go test ./...`、`go vet ./...`。集成数据库测试待专用新测试库可用后执行，不连接旧库。

## 2026-09-09 在线时长专项更新
- 后台保留当前 LinkGame 优化后的展示与查询；只补充“含广告、15秒”的口径提示。
- OnlineTimeAccumulator 使用前台帧时间与 SDK ad_show→ad_close/ad_fail 展示区间互斥计时，每15秒生成 online_time；普通后台不计入，广告脚本挂起在回调恢复后补记。保留不足15秒尾段。
- 展示失败不计广告时长、提前关闭计实际区间；重复回调不双计、切会话清理旧广告、缺终止回调上限30分钟。不要另把 adDurationMs 加进后台在线 SUM。
- 统计无法区分 SDK 未提供原因的广告遮罩隐藏与广告期间真正离开应用，因此指标定义为 SDK 展示区间，不宣称是纯视频观看秒数。真机需验证平台回调顺序。

## 2026-09-09 新环境 Web 测试交付授权（覆盖上述旧预留限制）
- lxy 已提供并授权接入、部署新后端/统计后台与可玩 Web 链接；本次可上传 linkgamebt/ 独立前缀、迁移独立新库、启动新服务。不提交、不推送，不操作旧项目目标。
- 运维实际库和用户均为 linkgamebt；只允许与指定 database-2 实例/3306 精确匹配，替代此前前缀预留要求。
- 共享 S3 桶/CDN 已获确认，新项目仅使用 linkgamebt/ 前缀。新 API 为 linkgamebt.bffbond.com，SSH 为 linkgamebt@18.183.189.2，端口 23001。
- 本次显式 web-preview + staging：启用原有测试账号云存档与 LocalPlatform 模拟广告流程。后续 TikTok 构建必须切回 tiktok-native，填平台配置并关闭测试登录；不允许静默降级。
- 广告规则、节点及 TikTok 后台添加由后续工作人员处理；本次不新增广告或改变奖励。

## 2026-09-09 最新第一版发布授权
- lxy 已提供新运维和 TikTok 应用参数，明确授权部署新服务端/Web、上传 TikTok Preview；覆盖前文默认不发布和参数待提供的旧状态。
- 当前新 staging 支持 TikTok 正式换票，并通过显式 allowWebPreview/BT_ALLOW_WEB_PREVIEW 保留 Web 测试身份；production 禁止测试身份。客户端 Native 无模拟兜底。
- 原广告触发点不变；策划后续提供新方案。本轮停止游戏浏览器测试，由 lxy 做真机操作验收；自动检查不能冒充真机通过。
