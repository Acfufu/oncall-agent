# Architect 独立复核（第二轮）

2026-10-06，read-only architecture_review。未运行测试。返回 REVISE，前轮10项中9项源码修正通过，分页第7项仍有阻断；不是最终批准。

PASS：运行期检索健康（先锁后remote，忙时0PUT、空间固定）、历史告警/不可变chunk标题、chat原query+environment、发布后取消投影恢复、metadata CAS、持久cleanup outbox、actual started重试计数、统一HTTP30s预算、M2潜在影响及来源检查器源码。

第7项阻断：
1. App.tsx对live API已筛选结果再次按starts_at与大小写敏感query筛选，与server first_received_at/lowercase口径不同；新收到但开始较早的firing和搜索cpu→CPUHigh会消失。应live直接消费服务器结果，demo才本地筛选，并补浏览器回归。
2. usePagedResource轮询只刷新第一页，保留旧后页却将全部标fresh。应刷新已加载页或明确重分页，并补后页恢复/撤下变化回归。

审查核对 targeted QA 9文件SHA全部匹配、green race/cleanup说明、L2 core PASS17.20s。最终全仓race、AM、修后分页浏览器和最终截图/性能仍待核验。主线程已将两个UI阻断交frontend所有者；修后必须再次独立复核。

随后主线程全包race发现新增budget测试在高负载时80ms父超时可能先于首embedding，测试无界channel读取挂起。已保留SIGQUIT栈与真实FAIL（full-race-budget-hang-red.log），改为有界、明确首调用失败断言及2s短父deadline；不改生产、不弱化504/单调用/SQL零chunks断言，重新验证。
