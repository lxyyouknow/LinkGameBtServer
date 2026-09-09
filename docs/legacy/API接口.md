# LinkGame 服务端 API

最后修改日期：`2026-09-08`

所有响应包含 `requestId`，并在响应头返回 `X-Request-ID`。受保护接口使用：

```http
Authorization: Bearer <LinkGame session token>
```

## 登录

### `POST /v1/auth/guest`

```json
{"installationId":"local-device-1234567890"}
```

### `POST /v1/auth/test-account`

当前无密码 Web 版本使用该入口；账号不是密码，不应复用真实密码：

```json
{"account":"lxy"}
```

### `POST /v1/auth/platform`

TikTok Native 静默登录；只有服务端同时开启开关并配置 Client Key/Secret 后可用：

```json
{"provider":"tiktok","code":"SDK 返回的一次性 code"}
```

三个接口成功均返回 `playerId`、只出现一次的明文 `token` 和 `expiresAt`。客户端不能上传
OpenID 或 playerId 代替平台验证。

## 云存档

### `GET /v1/save`

新号默认：`level=0`、主题 0 完整激活、`selectedTheme=0`、`collectingTheme=1`，三类道具
均为 0 个，全部设置开启。`level` 与客户端一致使用零基编号。响应固定包含
`claimedTikTokMissions` 数组，当前只可能出现 `home_shortcut:v1`、`profile_revisit:v1`，
用于跨设备恢复平台任务红点与已领取状态。

### `PUT /v1/save`

```json
{
  "revision": 1,
  "level": 2,
  "selectedTheme": 0,
  "collectingTheme": 1,
  "soundEnabled": true,
  "musicEnabled": true,
  "effectsEnabled": true,
  "vibrationEnabled": true,
  "tutorialCompleted": true,
  "coinMutations": [],
  "propMutations": [
    {
      "id": "session-1:hint-use:1",
      "propType": "hint",
      "delta": -1,
      "reason": "use",
      "createdAt": "2026-08-20T08:00:00Z"
    }
  ],
  "themeFragmentMutations": [
    {
      "id": "session-1:theme:1",
      "themeId": 1,
      "fragmentIndex": 5,
      "reason": "level_complete",
      "createdAt": "2026-08-20T08:01:00Z"
    }
  ],
  "clientVersion": "0.1.0"
}
```

规则：

- `revision` 必须等于云端版本；成功后加 1，冲突返回 `409 REVISION_CONFLICT`。
- 关卡和教程只前进不回退；选中主题使用最后一次成功提交值。
- `soundEnabled / musicEnabled / effectsEnabled / vibrationEnabled` 是旧接口兼容字段；当前客户端
  的音乐、音效和震动开关只保存在设备本地缓存，不以数据库值为权威。
- 金币、道具不能上传余额快照，只能提交 mutation；重试必须复用原 ID。
- `use` 道具只能 `-1`；广告正向奖励只由服务端广告会话写入 `ad_reward` 流水。
- 普通通关碎片不能发给限定主题；主题 0 默认完整、不写碎片流水。
- 选择非 0 主题前，服务端必须确认该主题碎片已经集齐。
- 主题碎片只增不减；普通主题 27 片、限定主题 34 片。
- 添加桌面新版固定流水为 `hint-v2 +3` 与 `theme-v2 +1`；Profile 新版固定流水为
  `remove +3` 与 `theme +1`。两项任务都会在同一事务写奖励、revision、审计流水和
  `claimedTikTokMissions`，重复/并发提交不会再次发奖。
- 服务端继续兼容旧版平台任务的 `coin +300 / hint +1 / shuffle +1` 固定流水；migration
  `000020`～`000021` 会分别把添加桌面与 Profile 的历史旧流水回填为已领取，新版不会向历史领取者重复补发。

## 总排行榜（2026-09-08，已随服务端 `20260908073231` 发布）

### `GET /v1/leaderboards/global?limit=30`

受保护只读接口。`limit` 缺省 30，只允许 1～30；返回按 `level DESC, reached_level_at ASC, player_id ASC` 排序的榜首列表和当前玩家 `self`。界面等级使用一基 `LEVEL N`，`self` 即使不在前 30 也会返回。未授权资料固定使用不可变公开编号生成 `USER000001` 格式昵称，并省略 `avatarUrl`。

```json
{"scope":"global","entries":[{"rank":1,"displayName":"USER000007","playerNumber":7,"level":168,"isSelf":false}],"self":{"rank":42,"displayName":"USER000042","playerNumber":42,"level":36,"isSelf":true},"serverTime":"2026-09-08T07:32:31Z"}
```

同一次请求的 `entries` 与 `self` 在同一 `REPEATABLE READ` 快照中计算，避免并发升关时相互矛盾。非法 `limit` 返回 `400 INVALID_ARGUMENT`。

### `POST /v1/player/profile/tiktok`

仅在玩家主动打开排行榜并由 TikTok 返回 `user.info.basic` 一次性授权 code 后调用：

```json
{"code":"TTMinis.game.authorize 返回的一次性 code"}
```

服务端换取短期 access token，验证 TikTok `open_id` 与当前 LinkGame Token 绑定身份一致，再调用官方 User Info 读取 `display_name` 与 `avatar_url`。Token、OpenID 和原始响应不落库、不写日志、不返回客户端。成功返回：

```json
{"status":"authorized","displayName":"PairMaster","avatarUrl":"https://example.invalid/avatar.png","updatedAt":"2026-09-08T07:32:31Z"}
```

上游 code/scope/撤权/响应错误使用 `PROFILE_*` 业务错误，不冒充 LinkGame `401`，避免客户端误刷新登录并重复消费一次性 code。昵称最多保留 32 个 Unicode 字符并移除控制字符；头像只保存合法 HTTPS URL。

## 游戏与广告统计

### `POST /v1/analytics/startup`（2026-09-08）

TikTok 登录前匿名启动诊断，无需 `Authorization`。每批 1～20 条，只允许 SDK 登录、服务端换票和首个可交互阶段；不得包含平台 code、Token、OpenID、账号或玩家 ID。接口返回 `202 Accepted`，客户端固定短超时且失败开放。

```json
{"events":[{"id":"startup:...","launchId":"launch:...","stage":"sdk_fail","eventTime":"2026-09-08T00:00:00.000Z","appId":1,"sdkType":1000,"channel":0,"clientVersion":"0.1.4","platform":"tiktok","os":"ios","system":"iOS_18.1","tiktokVersion":"45.9.0","sdkVersion":"0.31.0","errorCode":"TIKTOK_LOGIN_FAILED","durationMs":820}]}
```

成功响应：

```json
{"acceptedEventIds":["startup:..."],"requestId":"..."}
```

### `POST /v1/analytics/events`

单次 1～100 条，`id` 在同一玩家下幂等。公共字段：`id`、`name`、`eventTime`、
`appId=1`、`sdkType`、`channel`、`clientVersion`、`platform`、`gameSessionId`。

事件：

```text
session_start, enter_game, main_level_start, online_time,
level_start, first_click, first_match, progress_25, progress_50, progress_75,
level_pass, next_level, retry, level_fail,
theme_open, theme_select, prop_use,
ad_request, ad_create, ad_load, ad_show, ad_close,
ad_success, ad_fail, ad_reward_claim_success, ad_reward_claim_fail
```

在线时长每条 1～300 秒；关卡事件带零基 `level`；`theme_select` 带 `themeId`；
`prop_use` 带 `propType`。广告事件使用同一 `adAttemptId` 串联，并可带
`adDurationMs / adPreloaded / adResult`；失败事件带
`adFailureStage / adErrorCode / adSubErrorCode`。激励 `ad_success` 只认 SDK
`isEnded=true`，奖励是否真实到账由 `ad_reward_claim_success/fail` 单独记录。

`enter_game` 从客户端 `0.1.1` 起只在登录成功、Loading 完全退场且首页可交互后上报；
`main_level_start` 在主线开始操作被接受时上报。服务端兼容 `0.1.0` 与未标记旧客户端原先把
`enter_game` 用作主线开始的历史口径。

## 激励广告奖励

### `POST /v1/ads/rewarded/session`

```json
{"placement":"hint","businessKey":"hint:550e8400e29b41d4a716446655440000"}
```

创建当前玩家 10 分钟有效的一次性奖励会话。支持
`hint / shuffle / auto_remove / level_complete / daily_gift / daily_challenge_replay / season_makeup`。
补签业务键严格为 `season-makeup:<seasonKey>:<day>`；创建时已校验当前东京自然月、历史日期、未领取
和剩余次数，核销时仍会在事务内重新校验。

### `POST /v1/ads/rewarded/claim`

```json
{"sessionId":"ad_...","requestId":"ad-claim:550e8400e29b41d4a716446655440001","adAttemptId":"ad:sdk-attempt"}
```

核销会话并在事务内发奖、写 mutation、增加 revision，返回 `grantedRewards / save`。同会话只能领取一次；同 `requestId` 只重放第一次成功结果。旧业务位兼容缺省 `adAttemptId`；
`season_makeup` 必须传入与客户端广告生命周期统计相同的 `adAttemptId`，且同一玩家不能跨会话复用。

## 每日两阶段礼包

按 `Asia/Tokyo` 自然日计算，奖励由服务端生成并持久化。

### `GET /v1/daily-gift`

返回 `serverTime`、`dayKey`、`nextResetAt` 和当日唯一 V2 `gift`。
`baseRewards` 固定为刷新道具 ×1 与一块未收集限定主题碎片；
`bonusRewards` 固定为刷新道具 ×1 与另一块不同的未收集限定主题碎片。
两阶段奖励在首次查询时一并预选并持久化，同一玩家同一天重复查询返回相同 `offerId` 和奖励。

### `POST /v1/daily-gift/claim`

```json
{
  "offerId": "dg_20260825_a1b2c3",
  "requestId": "dg-claim:550e8400e29b41d4a716446655440000"
}
```

该接口免费发放且只发放 `baseRewards`，在同一事务中更新礼包、资产流水、主题碎片和存档
revision，并返回 `gift / grantedRewards / save`。相同 `requestId` 重试返回首次结果。

基础奖励领取后，客户端才可用 `daily_gift + offerId` 创建激励广告会话。广告核销接口只发放
`bonusRewards`；基础未领取、东京自然日已切换或追加奖励已领取时拒绝创建或核销。
`claimedAt` 与 `bonusClaimedAt` 始终返回；未领取阶段明确为 JSON `null`，不省略字段。

## 每日挑战（2026-08-31，已随服务端 `20260831143419` 发布）

按 `Asia/Tokyo` 自然日计算，普通主线达到 `LEVEL 10` 后解锁。服务端保存绝对
`challengeLevel`，实际专用关卡为 `challengeLevel % 34`。

### `GET /v1/daily-challenge`

返回 `dayKey / unlocked / challengeLevel / challengeIndex / completionCount / completedToday /
replayAvailable / activeAttempt / serverTime`。跨东京零点保留绝对进度，但清除旧日次数、资格和活动轮次。

### `POST /v1/daily-challenge/start`

```json
{"requestId":"daily-start:550e8400e29b41d4a716446655440000"}
```

免费轮或已核销的广告轮均由服务端判断，响应包含服务端生成的 `attemptId`、关卡、模式和固定
`rewards`。重复请求或失败/退出后重进返回同一 active attempt，不重新随机奖励；开始广告轮不消耗资格。

### `POST /v1/daily-challenge/complete`

```json
{"attemptId":"dc_attempt_...","requestId":"daily-complete:550e8400e29b41d4a716446655440001","clearSeconds":86}
```

服务端事务内按奖励快照发奖、完成轮次、推进挑战、消费广告资格并增加 save revision，返回
`attemptId / grantedRewards / challenge / save`。相同请求或已完成轮次重试只重放首次成功结果。

首次通关后，客户端以 `daily_challenge_replay + daily-challenge:<dayKey>:<challengeLevel>` 创建
一次性广告会话。只有 SDK `isEnded=true` 才核销；核销响应 `grantedRewards=[]` 并返回
`challenge.replayAvailable=true`，不增加库存或 save revision。

每日挑战 complete 成功事务还会以 `completionId=daily:<attemptId>` 幂等推进当日赛季通关任务；
客户端不得再调用普通关赛季上报接口。

## 自然月赛季与补签（2026-09-02，已随服务端 `20260902041826` 发布）

按 `Asia/Tokyo` 自然月计算，1～25 日开放当天任务和奖励，26 日至月末只允许查询整月进度。
服务端配置 `original-season-v1` 固定镜像客户端原版 12 个月、每月 25 日奖励。

### `GET /v1/season/current`

返回 `serverTime / timezone / seasonKey / configVersion / configId / themeId / startsAt / endsAt /
nextResetAt / currentRewardDay / stateVersion / claimedDays / makeupRemaining / today / rewardDays`。`rewardDays` 始终完整
返回 1～25 日；26 日后 `currentRewardDay` 和 `today` 明确为 `null`。

### `POST /v1/season/progress/level-clear`

```json
{"requestId":"season-clear:550e8400e29b41d4a716446655440000","completionId":"main:550e8400e29b41d4a716446655440001","level":53}
```

只供普通主线成功结算调用。`requestId` 与同一次关卡运行的 `completionId` 必须稳定复用；接口最多把
当天 `clearedLevels` 推进到 5，不发奖励。响应返回 `seasonKey / dayKey / stateVersion /
clearedLevels / claimable`。

### `POST /v1/season/tasks/login/claim`

### `POST /v1/season/tasks/clear_levels/claim`

```json
{"requestId":"season-claim:550e8400e29b41d4a716446655440002","expectedSeasonKey":"2026-09","expectedDayKey":"2026-09-01"}
```

登录任务查询后即可领取；通关任务达到 5 次后可领取。第一项领取只推进赛季状态，不发经济奖励；
第二项领取且另一项已领取时，在同一事务中发当天完整限定主题碎片和道具、写 mutation、增加 save
revision，并返回 `acceptedTask / grantedRewards / season / save`。相同 `requestId` 重放完整首次响应；
客户端不能提交奖励、库存、任务进度或已领取日期。

历史 1～25 日且早于服务器东京当日、尚未发奖的日期可通过 `season_makeup` 广告会话补签。
`makeupRemaining` 新月初始 5，每个新东京自然日首次读写只恢复 1，上限 5；核销在同一事务内扣 1、
按目标日固定 `rewardSnapshot` 发奖、标记该日已领、写广告和补签审计并返回完整 `season / save`。
稳定业务错误为 `SEASON_MAKEUP_LIMIT_REACHED / SEASON_MAKEUP_DAY_NOT_ELIGIBLE /
SEASON_DAY_ALREADY_REWARDED / SEASON_REWARD_PERIOD_CLOSED`。

## 统计后台

- 永久推荐页面：`GET /admin/latest`
- 兼容页面：`GET /admin.html`、`GET /admin`
- GM 登录：`POST /api/gm/login`
- 报表：`POST /api/gm/stats/overview|overview-trend|daily|levels|funnel|themes|props|ads|ad-failures|ranking|ad-ranking`
- 赛季玩家审计：`POST /api/gm/season/player`，请求体 `{"playerId":"公开玩家 ID"}`；返回当月
  25 日状态、通关事件、领取请求、补签恢复日与补签的目标日/session/adAttempt/businessKey/奖励快照/
  save revision，以及赛季奖励 mutation ID；不提供补发或回滚操作。

`ads` 与 `ad-failures` 按服务端接收日期、`clientVersion`、`platform`、广告形式和业务位分组；
失败明细继续按阶段、主错误码和 TikTok 子错误码拆分。

`overview` 自 `2026-09-07` 起与 HPGame 统一采用新增玩家 cohort 全生命周期口径：日期范围筛选
`analytics_player_stats.first_seen_date`，返回该批玩家截至查询时的累计登录、累计在线、进入游戏率、
激励展示/完播及人均完播。D1/D3/D7 分别判断所选玩家的 `last_login_date` 是否达到
`first_seen_date + 1/3/7 天`，三个指标都使用所选新增玩家数作为分母；晚于目标日回访仍满足对应累计留存。
旧的 `retentionObservationDate` 与 cohort 日期字段仅保留 JSON 兼容，新页面不再使用结束日观察口径。
`2026-09-05` 新增
`cohortMainLevelEnteredPlayers / mainLevelEnterRate`，按是否存在普通关卡
`level_start` 统计真实进入主线的玩家，包含新账号自动直达第 1 关；旧
`cohortMainLevelStartPlayers / mainLevelStartRate` 仍表示首页主线按钮操作并保留兼容。旧字段
`activePlayers / loginCount / enteredPlayers / averageOnlineSeconds` 继续保留为所选自然日内行为口径，
供旧页面兼容。

`overview-trend` 使用同一筛选条件返回 cohort 累计指标：多日范围按 `first_seen_date` 分日，单日范围
按玩家服务端首次接收时间 `analytics_player_stats.created_at` 换算到报表时区后分小时。每项通过
`granularity=day|hour` 标明粒度，`cohortDate` 在小时粒度时返回 `00时`～`23时`。指标包含新增人数、
人均登录/在线、进入游戏/主线率、D1/D3/D7、激励完播用户率、完播数、人均完播、奖励到账和插屏正常关闭。
小时数据使用已有服务端时间，不依赖客户端时钟，不需要新埋点、migration 或历史回填。

`ad-ranking` 同样按新增玩家 cohort 筛选，仅统计 `ad_format=rewarded` 且收到 `ad_success` 的唯一
广告尝试，最多返回 200 名玩家；请求、展示、提前关闭和奖励核销失败均不计入完播排行。

后台链接固定且禁止缓存。GM 密码只来自服务端环境变量。
