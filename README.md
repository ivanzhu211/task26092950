# qualityplan

QualityPlan 的版本感知执行汇总库（Go）。只处理一个计划，顺序调用，
不做持久化、并发或崩溃恢复。

## API

```go
p, _ := qualityplan.NewPlan(caseSuites, caseRevisions) // 用例→套件映射 + 各用例当前 revision
p.Activate(caseID, revision)                           // 切换/回滚当前修订
p.OpenCycle(cycleID, caseIDs)                          // 建立周期，冻结成员当时的 revision
p.Apply([]qualityplan.Correction{...})                 // 批量更正（可跨周期，NotRun 表示撤回）
p.Stats(qualityplan.StatsQuery{CycleID: nil, SuiteID: nil}) // 计划/套件/周期/周期内套件
```

状态：`NotRun`、`Pass`、`Fail`、`Blocked`、`Skipped`。统计含五种状态计数
（之和恒等于分母）及通过率 Pass/(Pass+Fail)、阻塞比例 Blocked/Total、
完成率 (Pass+Fail+Skipped)/Total，零分母比例为 0。

## 设计

- 每个用例按 revision 分桶，桶内用最大堆（`container/heap`，带索引）按周期
  序号组织已上报结果；计划/套件统计只维护各状态计数器，候选结果变化时
  O(log n) 增量调整。
- 周期统计只看自身冻结快照的计数器，不受 `Activate` 影响。
- 单条结果处理 O(log(E+1))，`Stats` 为 O(1)，活跃内存 O(C+E)，
  撤回即删除堆元素，不保留更正历史。

## 测试

```sh
go test ./...                      # 全部测试（含规模测试）
go test -short ./...               # 跳过规模测试
go test -run TestScale -v .        # 规模测试，报告耗时与内存
go test -run TestDifferentialAgainstReference -count=1 .  # 差分测试
```

- `plan_test.go`：两个 revision、三个周期的回滚/迟到更正/撤回场景与错误处理。
- `reference_test.go`：独立逐项重算参考实现 + 固定种子随机操作序列，
  每次操作后核对所有层级的计数与比例。
- `scale_test.go`：10 万用例、100 万周期用例对、单用例 10 万周期深历史、
  固定数据集上 50 万次撤回重报，报告耗时与堆内存变化。
