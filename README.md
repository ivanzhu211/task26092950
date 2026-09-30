# task26092950

QualityPlan 版本感知执行汇总库（Go）。

## 功能

初始化时固定用例到套件的映射及各用例当前 revision，提供四个操作：

- `Activate(caseID, revision)`：切换用例当前修订，允许切回历史修订。
- `OpenCycle(cycleID, caseIDs)`：为非空用例集合建立执行周期，冻结成员当时的
  revision；周期按创建顺序获得严格递增序号。
- `Apply(batch)`：批量更正周期内用例结果（Pass/Fail/Blocked/Skipped/NotRun），
  NotRun 表示撤回该位置已有结果；批次可跨周期。
- `Stats(cycleID, suiteID *string)`：返回计划、套件、周期或周期内套件的
  五种状态计数与比例（通过率、阻塞比例、完成率）。

计划/套件级统计中每个用例只贡献一次：取冻结 revision 等于当前 revision 且
结果非 NotRun 的周期中创建序号最大者的结果；无候选则为 NotRun。周期级统计
只看自身冻结快照，不受 Activate 影响。

## 设计要点

- 每个用例按 revision 维护「周期序号最大堆 + 有效结果表」，单条更正/撤回/
  切换的成本为 O(log(E+1))，E 为周期用例对总数。
- 所有层级计数均增量维护，Stats 为 O(1)，不遍历用例、周期成员或历史。
- 堆内每个序号至多入堆一次，撤回只做惰性删除，活跃内存为 O(C+E)，
  不随操作次数增长（C 为用例数）。

## 测试

```sh
go test ./...                                    # 全部测试
go test -run TestInterleavedRevisions -v         # 回滚/迟到更正/撤回场景
go test -run TestRandomizedCrossCheck -v         # 固定种子随机序列对拍逐项重算参考实现
go test -run TestScale -v                        # 10 万用例 / 100 万周期用例对
go test -run TestDeepHistorySingleCase -v        # 单用例 10 万周期深历史
go test -run TestChurnMemoryStability -v         # 固定数据集持续撤回重报的内存稳定性
go test -short ./...                             # 跳过规模测试，快速回归
```
