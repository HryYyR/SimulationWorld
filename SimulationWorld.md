微观生态模拟器（EcoSim）完整技术设计文档
目标：Go 实现 2D 局部原始生态模拟（草-鹿-虎最小闭环），支持天气/季节/神力干预/回放/调参，本文档为唯一构建依据。
文档约定：[R] = 必须实现的硬性规则；数值默认值统一收录在附录 A，正文引用槽位名。
目录
设计哲学与范围
顶层架构与包结构
并发模型
确定性规范（全项目最重要的一章）
核心数据模型
Tick 流水线
修饰器管道
环境层：日历 / 季节 / 天气
各系统详细规则
命令队列与神力
事件、快照、回放
观测层：账本 / 指标 / 曲线
无头模式与调参工具
渲染与 UI 层
测试策略
里程碑与验收标准
常见陷阱清单
构建顺序建议
附录：默认数值 / 配置文件全文
1. 设计哲学与范围
   1.1 两条底层流
   一切内容必须挂接到以下两条流之一，否则不许进入代码：
   能量流（单向）：太阳 → 草 → 鹿 → 虎 → 代谢耗散
   物质环（循环）：养分 → 草 → 动物 → 尸体/粪便 → 养分
   1.2 MVP 范围（闭环）
   草(生产者) → 鹿(食草) → 虎(食肉) → 尸体 → 养分 → 草
   ↑繁殖              ↑繁殖
   断环测试（验收依据）：去掉任一环，系统应在可观测时间内崩塌。
   1.3 明确排除（v1 不做）
   树子系统、演化/基因（仅预留数据结构）、双亲繁殖（预留配置位）、局部空间天气、网络功能、存档跨版本兼容。
   1.4 一个中心架构思想
   天气、季节、干旱、神力，架构上是同一类东西——都是“作用于确定性模拟内核的外部输入”，全部通过修饰器管道进入（§7）。
   架构验收标准：未来加“火山爆发”只需新增一个 JSON 配置 + 一个状态机，不修改任何已有系统代码。
2. 顶层架构与包结构
   2.1 分层图（依赖严格单向向下）
   ┌─────────────────────────────────────────────┐
   │ 表现层  cmd/simui (ebiten)   渲染/图表/UI    │ 只读快照，可整体重写
   ├─────────────────────────────────────────────┤
   │ 观测层  internal/observe     账本/指标/事件  │ 只读，内核无感知
   ├─────────────────────────────────────────────┤
   │ 干预层  internal/god         命令/回放控制   │ 只产出命令
   ├─────────────────────────────────────────────┤
   │ 环境层  internal/env         日历/天气发生器 │ 产出修饰器
   ├─────────────────────────────────────────────┤
   │ 内核    internal/core + systems + world      │ 确定性 tick 流水线
   ├─────────────────────────────────────────────┤
   │ 数据    cfg/*.json           物种/天气/平衡  │ 热加载(重建世界)
   └─────────────────────────────────────────────┘
   [R] 依赖规则：systems 可以 import world/env/modifier/observe/config/rng；world 不得 import systems；observe 不得 import systems；simui 只 import god + observe + config，绝不 import systems/world 内部符号（只通过快照读数据）。
   2.2 Go 包结构
   ecosim/
   ├── cmd/
   │   ├── sim/          # 无头运行器（也做回放校验）
   │   └── simui/        # ebiten 图形客户端
   ├── internal/
   │   ├── rng/          # 确定性哈希随机
   │   ├── config/       # 配置加载/校验/哈希
   │   ├── world/        # Grid / Animal / Corpse / World / 世界生成
   │   ├── modifier/     # 修饰器与参数解析
   │   ├── env/          # 日历、季节、天气马尔可夫链
   │   ├── systems/      # 流水线各系统（每系统一个文件）
   │   ├── core/         # Ctx / System 接口 / Scheduler / 流水线组装
   │   ├── god/          # 命令定义 / 命令队列 / 回放
   │   └── observe/      # 事件 / 能量账本 / 指标 / 状态哈希 / 快照
   ├── tools/sweep/      # 参数扫描工具
   ├── cfg/
   │   ├── balance.json  # 世界与草参数
   │   ├── species.json  # 物种定义
   │   └── weather.json  # 天气状态机
   └── testdata/         # 黄金回放基准
3. 并发模型
   goroutine A: 模拟循环（独占 World，内部单线程）
   │  发布不可变快照（atomic.Value 或带互斥的单槽）
   ▼
   goroutine B: ebiten 渲染循环（只读快照，永不触碰 World）
   │  UI 操作 → Command 结构体 → chan god.Command
   ▼
   goroutine A 在 tick 边界统一消费命令
   [R] 铁律：
   模拟内核零并发：systems 内禁止启动 goroutine、禁止 channel、禁止锁。确定性优先于性能。
   渲染与模拟的通信只有两条通道：快照（下行）与命令（上行）。
   UI 的暂停/加速属于客户端行为，不是模拟命令，不进命令日志。
   快照发布限频（≤60 次/秒），高速运行时允许跳帧——模拟速度不受渲染拖累。
4. 确定性规范（全项目最重要的规则）
   定义：同 seed + config + 命令日志 → 任意平台、任意运行次数 → 世界状态逐 bit 一致。
   4.1 哈希随机数（替代 math/rand）
   package rng
   func SplitMix64(x uint64) uint64 {
   x += 0x9E3779B97F4A7C15
   x = (x ^ (x >> 30)) * 0xBF58476D1CE4E5B9
   x = (x ^ (x >> 27)) * 0x94D049BB133111EB
   return x ^ (x >> 31)
   }
   func Hash(seed uint64, parts ...uint64) uint64 {
   h := seed
   for _, p := range parts {
   h = SplitMix64(h ^ (p * 0xD6E8FEB86659FD93))
   }
   return h
   }
   // 流编号：语义分域，避免不同用途互相干扰
   const (
   StreamWorldGen uint64 = 1 // 世界生成
   StreamWeather  uint64 = 2
   StreamBehavior uint64 = 3 // 动物决策
   StreamLifespan uint64 = 4 // 出生时寿命抽样
   StreamOrder    uint64 = 5 // tick 内处理顺序
   StreamRepro    uint64 = 6
   )
   type Rng struct{ key, ctr uint64 }
   func New(seed uint64, stream uint64, tick, id int) *Rng {
   return &Rng{key: Hash(seed, stream, uint64(tick), uint64(id))}
   }
   func (r *Rng) Uint64() uint64 { r.ctr++; return SplitMix64(Hash(r.key, r.ctr)) }
   func (r *Rng) Float64() float64 { return float64(r.Uint64()>>11) / (1 << 53) }
   func (r *Rng) Intn(n int) int   { return int(r.Uint64() % uint64(n)) }
   [R] 关键性质：随机数以 (seed, stream, tick, entityID, counter) 为键 → 结果与遍历顺序无关 → 未来加系统不会悄悄改变所有随机结局。
   [R] 禁止：全局 math/rand、time.Now()、os.Getpid() 等一切非确定输入进入模拟代码。随机数只能通过 rng 包获取。
   4.2 处理顺序
   资源争夺（两只鹿抢同一格草）需要确定顺序。[R] 每个行为 tick，按以下顺序遍历动物：
   sort.Slice(animals, func(i, j int) bool {
   return Hash(seed, StreamOrder, uint64(tick), uint64(animals[i].ID)) <
   Hash(seed, StreamOrder, uint64(tick), uint64(animals[j].ID))
   })
   顺序看似随机、实则确定，且与实体插入顺序无关。
   4.3 禁止遍历 Go map
   [R] Go map 迭代顺序随机。模拟逻辑中禁止 for k := range someMap 参与任何状态变更。方案：主存储用 []*Animal 切片（删除时过滤重建），仅用 map[int]*Animal 做 ID 索引、从不迭代它。
   4.4 浮点
   模拟层统一 float64（Go 基本运算跨平台确定）；float32 只允许出现在渲染缓冲。
   禁止 math 包以外的三方数值库；JSON 配置里浮点数解析进 float64 是确定的。
   4.5 规则版本
   [R] config.rules_version 写入每个存档/回放。改流水线顺序、公式、事件语义时必须递增。回放遇到版本不匹配直接报错，不做兼容。
5. 核心数据模型
   5.1 世界常量
   项	值	说明
   网格	64×64	row-major 平铺数组
   1 tick	1 天		
   1 季	100 tick		
   1 年	400 tick（四季）		
   距离	切比雪夫距离	移动为 8 方向
   5.2 Grid（SoA 平铺，缓存友好）
   package world
   type Grid struct {
   W, H     int
   Grass    []float64 // 0..100，草量
   Nutrient []float64 // 0..100，土壤养分
   }
   func (g *Grid) Idx(x, y int) int     { return y*g.W + x }
   func (g *Grid) InBounds(x, y int) bool
   5.3 实体
   type Animal struct {
   ID       int
   Species  string // "deer" | "tiger"，作为 species.json 的键
   X, Y     int
   Age      int     // tick
   Energy   float64 // 0..100（上限可配置）
   Lifespan int     // 出生时抽样：Uniform[lifespan_min, lifespan_max]
   Cooldown int     // 距下次可繁殖的剩余 tick
   Dead     bool    // resolveDeaths 阶段统一清理
   // 预留（v1 不用）：Genes map[string]float64 —— 个体对参数槽位的乘性覆盖
   }
   type Corpse struct {
   ID        int
   Species   string
   X, Y      int
   Total     float64 // 总养分量
   Remaining float64
   TotalTicks int
   TicksLeft  int
   }
   [R] 个体参数公式（含未来演化）：
   生效值(slot, 实体) = config基础值(slot) × Π全局修饰器 × Π局部修饰器(该坐标) × 实体基因覆盖(预留,恒为1)
   5.4 World
   type World struct {
   RulesVersion int
   Tick         int
   Seed         uint64
   Grid         Grid
   Animals      []*Animal
   Corpses      []*Corpse
   byID         map[int]*Animal // 仅索引，从不迭代
   NextID       int
   Weather      env.WeatherState // 当前天气与剩余 tick
   // Season 由 Tick 推导，不存储（单一事实来源）
   }
   5.5 世界生成（world.Gen）
   全部使用 rng.New(seed, StreamWorldGen, 0, 0)：
   每格：nutrient = 40 ± 10 噪声，grass = 30 ± 10 噪声（clamp 到 [0,100]）
   随机位置撒 24 鹿、4 虎（互不重叠即可）；鹿初始 energy=60，虎=80；每只抽样 Lifespan（StreamLifespan，key=其 ID）
   天气初始：sunny，duration 抽样
   5.6 空间索引（接口预留，实现先暴力）
   // systems 内查询走此接口；当前规模(≤500动物)下 O(n²) 完全够用
   type SpatialIndex interface {
   AnimalsInRadius(x, y, r int, species string) []*world.Animal
   }
   破 2000 只再换网格桶实现；接口不变，系统代码不动。
6. Tick 流水线
   6.1 System 接口与调度器
   package core
   type Ctx struct {
   Seed   uint64
   Tick   int
   Cfg    *config.Root
   Params *modifier.Table   // 本 tick 全局生效参数（resolveModifiers 产物）
   Scoped []modifier.Scoped // 局部修饰器小列表（供 ResolveAt）
   Ev     *observe.Emitter  // 事件发射
   Ledger *observe.Ledger   // 能量账本
   }
   type System interface {
   Name() string
   Step(w *world.World, c *Ctx)
   }
   type Scheduler struct{ systems []System }
   func (s *Scheduler) Run(w *world.World, c *Ctx) {
   for _, sys := range s.systems {
   sys.Step(w, c)
   }
   }
   [R] 系统之间禁止互相调用；只通过 World 读写、通过 Ctx 取参数/发事件。加系统 = 在组装函数中插入一项。
   6.2 流水线顺序（属于“规则”本身，改动需递增 rules_version）
0.  applyCommands    神之命令（tick 边界统一应用，见 §10）
1.  advanceCalendar  tick++；季节推导；季节切换发事件
2.  stepWeather      天气状态机推进（§8）
3.  resolveModifiers 汇总天气/季节/神力修饰器 → Params + Scoped（§7）
4.  growGrass        草生长，消耗养分
5.  decayCorpses     尸体腐烂 → 养分
6.  rebuildIndex     空间索引重建
7.  behave           动物：感知→决策→行动（§4.2 确定乱序）
8.  metabolism       基础代谢扣能量（幼年系数、季节/天气修饰生效）
9.  reproduce        繁殖检查 → 产仔
10. resolveDeaths    饿死/老死 → 转尸体；清理 Dead 实体
11. ledgerAndMetrics 记账核对、指标采样、事件刷盘、定时快照
    顺序为什么重要：先长草后进食，意味着“今天的草今天可吃”；若反过来会改变整个种群动力学。顺序即规则。
7. 修饰器管道
   7.1 参数槽位命名规范
   [R] 全项目唯一命名法 "<owner>.<param>"：
   槽位	基础值来源
   grass.growth_mult	balance.json（=1.0，纯修饰目标）
   deer.metabolism, tiger.metabolism	species.json
   deer.move_cost, tiger.move_cost	species.json
   deer.eat_threshold, …	species.json
   tiger.hunt_success	species.json
   deer.reproduce_threshold, …	species.json
   [R] 任何系统读参数必须走 c.Params / c.ResolveAt(...)，禁止直接读 config 原值参与运算（config 只是基础值的存放处）。
   7.2 数据结构
   package modifier
   type Modifier struct {
   Key      string  // "grass.growth_mult"
   Mult     float64 // 乘性
   Add      float64 // 加性（慎用）
   Source   string  // "weather.rain" / "season.winter" / "god.cmd#7"
   Priority int     // 同 key 多源时的结算次序
   }
   type Scoped struct {
   Modifier
   X, Y, R int // 圆形作用域；R<=0 表示全局
   }
   // TTL 型（神力脉冲用）：每 tick 在 resolveModifiers 中统一衰减
   type Timed struct {
   Modifier
   TTL int
   }
   7.3 结算公式与时机
   每个 tick 在 resolveModifiers（流水线 #3）一次性结算并缓存：
   global(slot) = (base(slot) + Σ Add) × Π Mult        // Add 按 (Priority, Source) 排序后累加（保证确定）
   at(x,y,slot) = global(slot) × Π(命中该坐标的 Scoped 的 Mult)
   连续源（季节、天气）：resolver 直接查询 env 层当前活跃的修饰器集合，无 TTL 管理。
   TTL 源（神力脉冲）：维护一个 []Timed 列表，每 tick 递减、归零移除（在 resolveModifiers 内完成，因此神力效果从命令应用的下一 tick 生效）。
   [R] 修饰器数组必须按 (Key, Priority, Source) 排序后再结算——乘法可交换，但 Add 存在时顺序影响结果。
   [R] 热路径提示：growGrass 遍历 4096 格，先取出 Scoped 中 key 为 grass.growth_mult 的子列表（通常 0~2 个）再进循环，禁止在格循环内做 map 查询/排序。
   7.4 预期效果（架构自检用例）
   雨：grass.growth_mult ×1.8（全局，连续）
   冬：grass.growth_mult ×0.25、deer.metabolism ×1.3（连续）
   风暴：deer.move_cost ×1.5、tiger.hunt_success ×0.6
   神之局部降雨：grass.growth_mult ×2.5，圆形作用域 r=5，TTL=10
   干旱传导链（不加任何新系统即可复现）：grass ×0.4 → 鹿能量下滑、繁殖推迟、饿死 → 鹿减少但虎有滞后（先吃存量）→ 虎随后饿死潮 → 雨季尸骸养分+草反弹 → 鹿回升。
8. 环境层：日历 / 季节 / 天气
   8.1 日历（无状态，纯推导）
   func SeasonOf(tick int, perSeason int) Season { // Season: spring/summer/autumn/winter
   return Season((tick / perSeason) % 4)
   }
   季节本身不存状态，只在 resolveModifiers 时作为连续修饰源被查询；季节切换时由 advanceCalendar 发 season_changed 事件。
   8.2 天气：马尔可夫状态机
   type WeatherState struct {
   Current string // sunny | rain | drought | storm
   Left    int    // 当前天气剩余 tick
   }
   func StepWeather(w *world.World, c *core.Ctx) {
   r := rng.New(c.Seed, rng.StreamWeather, w.Tick, 0)
   if ws.Left > 0 { ws.Left--; return }
   next := weightedPick(Transitions[season][ws.Current], r) // 转移矩阵
   dur  := r.Intn(max-min) + min                            // 各状态持续区间不同
   ws.Current, ws.Left = next, dur
   c.Ev.Emit("weather_changed", ...)
   }
   转移矩阵按季节调制（附录 A.3）：夏季干旱概率高、冬季风暴概率高、春季多雨。
   干旱是长持续状态（30~80 tick），这就是天然的种群外部压力源。
   天气修饰器集合定义在 weather.json，resolver 按 Current 查询（连续源，无 TTL）。
9. 各系统详细规则
   9.1 growGrass（流水线 #4）
   对每格：
   growth = (growth_base + nutrient × growth_nutrient_coeff) × c.ResolveAt("grass.growth_mult", x, y)
   newGrass = min(growth_cap, grass + growth)
   actual = newGrass - grass
   grass = newGrass
   nutrient = max(0, nutrient - actual × nutrient_consumption_coeff)   // ← 物质环的出口
   Ledger.Add("solar.grass", actual)
   渲染上草量→绿色亮度、养分→棕色深浅，使“坟场绿洲”肉眼可见。
   9.2 decayCorpses（#5）
   对每具尸体（每 tick 释放 Total/TotalTicks）：
   release = corpse.Total / corpse.TotalTicks
   cell.nutrient += release × 0.7                    (clamp 100)
   8 邻居各 += release × 0.3 / 8
   TicksLeft--, Remaining -= release
   if TicksLeft == 0: 移除尸体，发 corpse_decayed 事件
   9.3 behave（#7）——动物 FSM
   鹿决策优先级（从上到下短路）：
1. FLEE    ：切比雪夫 ≤ threat_radius(4) 内有虎 → 朝远离虎方向移动 1 格
2. EAT     ：当前格 grass ≥ eat_rate(4) 且 Energy < energy_cap(100)
   → 吃 min(eat_rate, grass)；Energy += 实吃 × efficiency(0.7)；
   本格 nutrient += dung(1)；Ledger 记 grass→deer 与耗散
3. FORAGE  ：Energy < eat_threshold(60)
   → 视野(6)内找草量最高格（并列取最近），移动 1 格；无目标则游荡
4. REPRODUCE：Energy ≥ 80 且 Cooldown==0 且 Age ≥ mature_age(40)
   → 产 1 崽于本格；Energy -= 45；Cooldown = 40；发 born 事件
5. WANDER  ：50% 概率随机方向移动 1 格
   虎：
1. REPRODUCE：Energy ≥ 90 且 Cooldown==0 且 Age ≥ mature_age(80)
   → 产 1 崽；Energy -= 60；Cooldown = 90
2. HUNT    ：视野(8)内最近鹿 → 移向它 1 格
   若已相邻（切比雪夫 ≤ 1）→ 结算捕猎（下）
3. WANDER  ：随机走
   捕猎结算（p = Params["tiger.hunt_success"]，天气已乘入）：
   成功（概率 p）：
   鹿 → Dead(cause=predated)；虎 Energy += min(55, energy_cap - Energy)；
   在鹿的位置生成鹿尸（nutrient 30 / 30 ticks）；发 hunt(success) 事件
   失败：
   鹿被弹开 2 格（随机方向，StreamBehavior 的鹿 RNG）；
   虎 Energy -= 2（扑空消耗）；鹿 Energy -= 1；发 hunt(fail) 事件
   [R] 移动成本在移动发生处立即扣除（Energy -= move_cost），基础代谢由 #8 扣——能量扣减点即其语义来源，便于账本对账。移动越界 = 不动（照扣不扣成本由实现定，必须写进 rules_version 对应文档：建议不扣）。
   [R] 所有决策随机数：rng.New(seed, StreamBehavior, tick, animalID)。
   9.4 metabolism（#8）
   mult = (Age < mature_age) ? juvenile_metabolism_mult(0.8) : 1.0
   Energy -= Params["<sp>.metabolism"] × mult
   9.5 reproduce（#9）
   补充处理 behave 中未覆盖的情况：本系统只做“冷却递减 + 兜底检查”，繁殖动作已在 behave 的 REPRODUCE 分支完成（同一 tick 内行为与繁殖一体决策，避免同 tick 双写）。冷却在此处递减。
   若实现时发现两处耦合不清，统一原则：一切改变世界状态的动作只发生在 behave 与 resolveDeaths 两个系统里，其他系统只做数值推进。
   9.6 resolveDeaths（#10）
   for animal（按 ID 升序遍历）:
   if !Dead && Energy <= 0   → Dead=true, cause=starved
   if !Dead && Age >= Lifespan → Dead=true, cause=old_age
   if Dead → 生成尸体（species.corpse 配置）；发 died 事件；Ledger 记残余能量去向
   过滤重建 Animals 切片与 byID；Age++ 在本系统末尾统一执行（下一个 tick 生效）
   捕杀死亡已在 behave 内就地处理（生成尸体+事件），此处跳过已 Dead 者，防重复。
   9.7 幼年机制（带来种群响应滞后，v1 即包含）
   Age < mature_age 的个体：不能繁殖、代谢 ×0.8。仅此两条，不加生命阶段枚举。
10. 命令队列与神力
    10.1 Command
    package god
    type Command struct {
    ID   int    // 全局递增，决定同 tick 内应用顺序
    Tick int    // 命令在该 tick 边界（流水线 #0）应用
    Type string // "spawn" | "remove" | "weather_force" | "param_mod"
    Spawn        *SpawnPayload   // {Species, X, Y}
    Remove       *RemovePayload  // {EntityID}
    WeatherForce *WeatherPayload // {State, Ticks}
    ParamMod     *ParamPayload   // {Key, Mult, TTL, X, Y, R(0=全局)}
    }
    [R] 神永远不在 tick 中途改状态。UI → chan Command → 模拟 goroutine 缓冲 → 下个 tick 的 #0 阶段，按 (Tick, ID) 排序应用，并写命令日志（ndjson）+ command_applied 事件。
    10.2 免费获得的三件事
    回放：{seed, rules_version, config_hash, 命令日志} = 完整重现一局
    撤销：弹掉最后一条命令 → 从 ≤ 目标 tick 的最近快照重放到目标点
    分享：命令日志文件即“神迹录像”
11. 事件、快照、回放
    11.1 事件
    type Event struct {
    Tick int
    Type string // born/died/hunt/weather_changed/season_changed/
    // corpse_decayed/command_applied
    A, B int    // 主体/客体实体 ID
    Val  float64
    }
    内存环形缓冲（最近 5000 条，供 UI 事件流）+ 无头模式下 ndjson 落盘。事件是 UI 事件流与统计的共同数据源——一份投入两处回报。
    11.2 快照
    每 500 tick：World 全量序列化（encoding/gob）→ 内存保留最近 8 份 + 磁盘
    快照含 Seed / RulesVersion / Tick / Grid / Animals / Corpses / Weather / NextID / 命令日志水位
    [R] 快照期间不改 World（模拟单线程天然满足）
    11.3 回放校验
    cmd/sim -replay tape.json：按 seed+config+命令日志重跑 N tick，逐快照比对 StateHash（§12.3），不一致即失败退出码非 0。
12. 观测层
    12.1 能量账本（Ledger）
    每 tick 记录流量对：
    solar.grass      （草实际生长量）
    grass.deer       （鹿实吃 × 1，鹿得 × efficiency）
    grass.dissip     （消化损耗）
    deer.tiger       （捕猎成功转移量）
    deer.dissip      （代谢+移动+繁殖开销）
    tiger.dissip     （同上）
    corpse.residual  （死亡时残余能量 → 抽象为养分，账本记为跨域出口）
    ledgerAndMetrics 系统末尾做守恒断言（容差 ε=1e-6×总量）：Δ系统总能 ≈ 流入 − 流出。守恒被破坏 = 数值泄漏 = 立刻 panic/log。它是 debug 工具，也是现成的能量桑基图数据源。
    12.2 种群指标（每 tick 采样，环形缓冲 20000 + CSV 导出）
    tick / deer_pop / tiger_pop / grass_total / nutrient_avg /
    births / deaths_starved / deaths_old / deaths_predated / kills
    12.3 状态哈希（黄金测试与回放校验的基石）
    func StateHash(w *world.World) uint64 {
    // FNV-1a 依次喂入：tick、Grid 两数组（row-major）、
    // 动物按 ID 升序的 (id,species,age,energy,x,y)、天气状态
    // 全部通过 binary.LittleEndian 写入 float64/int64 字节
    }
    [R] 哈希前必须排序——map/切片原序不可信。
13. 无头模式与调参工具
    13.1 cmd/sim（无头）
    sim -seed 20260901 -ticks 10000 -cfg cfg/ -out result/
    输出：population.csv / events.ndjson / 末态哈希 / 生存报告 JSON
    [R] 模拟核心不依赖任何渲染——systems 包不 import 任何图形库，这是无头与扫参的前提。
    13.2 tools/sweep（参数扫描）
    sweep -vary "tiger.hunt_success=0.2:0.6:0.1,deer.metabolism=1.0:1.6:0.2" \
    -seeds 20 -ticks 10000 -out sweep.csv
    每次运行独立世界 → 并行安全（errgroup + worker pool，这是全项目唯一允许并发的位置）
    输出列：参数组合 / seed / 存活 tick / 各物种 min/max/mean / 是否灭绝 / 振荡峰数
    13.3 平衡的显式定义（否则无可优化目标）
    指标	达标线
    10000 tick × 20 seeds	≥90% 局无灭绝
    鹿种群	min ≥ 5，max ≤ 120
    虎种群	min ≥ 1
    3000 tick 内鹿-虎振荡	≥3 轮，相位错开
    鹿 max/min 比值	≤ 30（防灭绝级崩塌）
    单 tick 耗时	< 5ms（pprof 验证）
    13.4 调参速查（症状 → 旋钮）
    症状	旋钮
    虎 500 tick 内灭绝	tiger.hunt_success ↑ 至 0.5 或 tiger.metabolism ↓ 至 2.0
    鹿爆炸后草清零大崩	grass.growth_base ↓ 或 deer.metabolism ↑
    系统死水（无波动）	检查天气/季节修饰器是否真的接入了 Params
    每轮波动都灭绝	reproduce.cost ↑ 或 grass.growth_cap ↑（加大缓冲）
14. 渲染与 UI 层（ebiten）
    14.1 职责边界
    只做三件事：画世界、发命令、展示观测数据。禁止在 UI 内做任何模拟计算。
    14.2 视觉映射
    数据	呈现
    grass	像素绿色通道（0-100 → 亮度）
    nutrient	棕色叠加（坟场绿洲可见）
    鹿/虎	浅棕/橙色圆点，尺寸随 Age
    尸体	灰点，透明度随 TicksLeft
    天气	全屏叠加（雨=竖线粒子、旱=暖色滤镜、风暴=抖动）
    14.3 UI 组件
    种群曲线（主界面常驻，最重要）：最近 3000 tick 的鹿/虎/总草量折线
    事件流：右侧滚动列表（“T2 捕杀了 D14”）
    检查器：点击动物 → 能量/年龄/状态面板；点击格子 → 草/养分数值
    速度：暂停 / 1× / 10× / 100× / MAX
    神力面板：放置鹿/虎（点击）、局部降雨（拖圆，发 param_mod 命令）、强制天气按钮（weather_force）
    14.4 主循环骨架
    func (g *Game) Update() error {          // ebiten 每帧调用
    g.view = g.latest.Load().(*Snapshot) // 原子读快照
    for _, cmd := range g.pendingUICommands() {
    g.cmdCh <- cmd                   // 上行命令
    }
    return nil
    }
    func (g *Game) Draw(screen *ebiten.Image) { /* 从 g.view 画 */ }
15. 测试策略
    测试	内容	频率
    黄金回放	固定 seed+config 跑 1000 tick → StateHash 与 testdata/golden.json 比对；-update 刷新基准	每次提交。改规则先递增 rules_version 再刷新
    顺序无关性	打乱命令到达顺序（命令带 Tick 戳），重放哈希不变	回归
    账本守恒	10000 tick 无泄漏 panic	回归
    单元测试	rng 分布粗检、modifier 结算、FSM 决策表（给定感知→断言动作）、decay 总量守恒	每系统
    冒烟	sim -ticks 100 退出码 0	CI
16. 里程碑与验收
    里程碑	内容	验收
    M0 骨架	rng/config/core/scheduler + 空世界跑 1000 tick	黄金测试框架跑通
    M1 草	growGrass + 渲染网格	草量曲线随养分衰减而放缓
    M2 单环	鹿 + 尸体 + 粪便	坟场绿洲出现；500 tick 鹿不灭
    M3 双环	虎 + 捕猎	3000 tick ≥3 轮振荡；固化黄金哈希
    M4 环境	季节 + 天气马尔可夫 + 修饰器	干旱剧情链路（§7.4）肉眼可复现
    M5 神力	命令队列 + 回放 + 撤销	回放哈希一致；撤销可回退
    M6 平衡	ledger 断言 + sweep 工具	§13.3 达标线全绿
    v2+	树子系统（遮光×0.2、结果、发芽、倒木慢速养分库）、演化（启用 Genes 覆盖）、双亲繁殖（mating_mode:"pair"）、腐食者、疾病	—
17. 常见陷阱清单（Go 特化）
    map 迭代随机 → 模拟逻辑禁 range map（§4.3）
    全局 math/rand / time.Now 混入 → 只用 rng 包（§4.1）
    系统内开 goroutine → 确定性杀手，零容忍（§3）
    tick 中途改参数/状态（UI 直改 World）→ 一切走命令（§10）
    快照哈希前不排序 → 假失败（§12.3）
    系统间函数互调 → 只通过 World/Ctx（§6.1）
    渲染层 import 内核类型 → 只 import 快照结构（§2.1）
    改了流水线顺序不递增 rules_version → 旧回放静默错乱（§4.5）
    Scoped 修饰器在 4096 格循环内做排序/map 查询 → 热路径（§7.3）
    float32/64 混用 → 模拟层 float64（§4.4）
18. 构建顺序建议（文件级）
    internal/rng + 单测
    internal/config + cfg/balance.json（附录 A.1）
    internal/world（Grid/Animal/Corpse/World/Gen）
    internal/modifier（含 Table 结算 + 排序）
    internal/core（Ctx/System/Scheduler/组装函数）
    internal/observe（Emitter/Ledger/Metrics/StateHash）
    cmd/sim 无头 + 黄金测试
    systems 依次：growGrass → decayCorpses → behave(仅鹿) → metabolism → resolveDeaths
    internal/env（日历/天气）+ 接入修饰器 → cfg/weather.json
    internal/god 命令队列
    cmd/simui（ebiten，最小：网格+曲线+暂停/加速）
    behave 加虎与捕猎 → M3 验收
    tools/sweep → M6
    第一步行动：完成 1~7 后，用无头模式跑“只有草的世界”1000 tick，确认黄金哈希在两次运行间一致——这是整个确定性地基的验收点。
19. 附录
    A.1 cfg/balance.json
    {
    "rules_version": 1,
    "world": { "width": 64, "height": 64, "seed": 20260901 },
    "time":  { "ticks_per_season": 100 },
    "grass": {
    "growth_base": 0.2,
    "growth_nutrient_coeff": 0.004,
    "growth_cap": 100,
    "nutrient_consumption_coeff": 0.05,
    "nutrient_cap": 100
    },
    "init": { "grass": 30, "nutrient": 40, "noise": 10, "deer": 24, "tiger": 4 },
    "energy_cap": 100
    }
    A.2 cfg/species.json
    {
    "deer": {
    "vision": 6, "threat_radius": 4,
    "move_cost": 0.5, "metabolism": 1.2,
    "diet": { "rate": 4, "efficiency": 0.7, "dung_nutrient": 1 },
    "eat_threshold": 60,
    "reproduce": { "energy_threshold": 80, "cooldown": 40, "cost": 45,
    "child_energy": 30, "mature_age": 40, "mating_mode": "asexual" },
    "lifespan": [200, 320], "juvenile_metabolism_mult": 0.8,
    "corpse": { "ticks": 30, "nutrient": 30 }
    },
    "tiger": {
    "vision": 8, "move_cost": 0.8, "metabolism": 2.2,
    "hunt": { "success": 0.35, "gain": 55, "fail_tiger_cost": 2,
    "fail_deer_cost": 1, "flee_jump": 2 },
    "reproduce": { "energy_threshold": 90, "cooldown": 90, "cost": 60,
    "child_energy": 40, "mature_age": 80, "mating_mode": "asexual" },
    "lifespan": [320, 480], "juvenile_metabolism_mult": 0.8,
    "corpse": { "ticks": 40, "nutrient": 50 }
    }
    }
    A.3 cfg/weather.json（矩阵行=当前态，列=下一态，顺序 sunny/rain/drought/storm）
    {
    "states": ["sunny","rain","drought","storm"],
    "duration": { "sunny":[5,15], "rain":[3,8], "drought":[30,80], "storm":[2,5] },
    "transitions": {
    "spring": { "sunny":[0.50,0.35,0.05,0.10], "rain":[0.55,0.30,0.05,0.10],
    "drought":[0.60,0.30,0.00,0.10], "storm":[0.60,0.25,0.05,0.10] },
    "summer": { "sunny":[0.55,0.15,0.25,0.05], "rain":[0.50,0.20,0.25,0.05],
    "drought":[0.40,0.20,0.35,0.05], "storm":[0.55,0.20,0.15,0.10] },
    "autumn": { "sunny":[0.55,0.25,0.10,0.10], "rain":[0.50,0.30,0.10,0.10],
    "drought":[0.55,0.25,0.10,0.10], "storm":[0.55,0.25,0.10,0.10] },
    "winter": { "sunny":[0.45,0.15,0.10,0.30], "rain":[0.45,0.20,0.10,0.25],
    "drought":[0.55,0.15,0.10,0.20], "storm":[0.45,0.15,0.10,0.30] }
    },
    "modifiers": {
    "rain":    [ {"key":"grass.growth_mult","mult":1.8},
    {"key":"tiger.hunt_success","mult":0.7} ],
    "drought": [ {"key":"grass.growth_mult","mult":0.4} ],
    "storm":   [ {"key":"deer.move_cost","mult":1.5},
    {"key":"tiger.hunt_success","mult":0.6} ],
    "sunny":   []
    },
    "season_modifiers": {
    "spring": [ {"key":"grass.growth_mult","mult":1.3} ],
    "summer": [],
    "autumn": [ {"key":"grass.growth_mult","mult":0.7} ],
    "winter": [ {"key":"grass.growth_mult","mult":0.25},
    {"key":"deer.metabolism","mult":1.3} ]
    }
    }
    A.4 数值速查（均为起点，以 sweep 结果为准）
    项	值
    鹿视野/威胁半径/代谢/移动成本	6 / 4 / 1.2 / 0.5
    鹿进食	4草 → 2.8 能量（效率0.7），排粪 +1 养分
    鹿繁殖	≥80 能量，冷却40，成本45，幼崽30能量，40天成熟
    鹿寿命	200~320 tick
    虎视野/代谢/移动成本	8 / 2.2 / 0.8
    捕猎	相邻成功率 35%，成功 +55；失败虎 -2、鹿 -1 且弹开2格
    虎繁殖	≥90，冷却90，成本60，80天成熟；寿命 320~480
    鹿尸/虎尸	30养分/30tick；50养分/40tick
    尸体分配	70% 本格 + 30% 八邻均分
    A.5 预期涌现现象（M3 验收观察项）
    鹿峰→虎峰→鹿谷→虎饥荒的相位错开循环
    鹿过量啃秃草地后的饿死潮
    坟场绿洲（饿死潮区域养分暴涨→草先绿→吸引鹿）
    无代码的“虎领地”（虎追鹿、鹿追草、草聚于猎杀点）
    这份文档可直接作为施工图。建议把 §4（确定性）和 §6.2（流水线顺序）打印贴在显示器旁——它们是后续一切分歧的仲裁标准。需要我继续输出某个包的完整可编译代码（如 rng+core+growGrass 的最小骨架），随时说。