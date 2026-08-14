# OpsOrigin API 路由清单

> 由 `tools/routes` 自动生成(`make routes` 重新生成), 共 **509** 个端点。
> 方法与路径为 Go 1.22 ServeMux 注册形态。

## `/api/v1/agent-tasks` (1)

| Method | Path | 位置 |
| --- | --- | --- |
| GET | `/api/v1/agent-tasks/{task_id}` | `internal/agentmgr/service.go:924` |

## `/api/v1/agents` (20)

| Method | Path | 位置 |
| --- | --- | --- |
| GET | `/api/v1/agents` | `internal/agentmgr/service.go:875` |
| POST | `/api/v1/agents` | `internal/agentmgr/service.go:874` |
| DELETE | `/api/v1/agents/{id}` | `internal/agentmgr/service.go:878` |
| GET | `/api/v1/agents/{id}` | `internal/agentmgr/service.go:876` |
| PUT | `/api/v1/agents/{id}` | `internal/agentmgr/service.go:877` |
| GET | `/api/v1/agents/{id}/circuit-breaker` | `internal/agentmgr/service.go:889` |
| PUT | `/api/v1/agents/{id}/circuit-breaker` | `internal/agentmgr/service.go:890` |
| POST | `/api/v1/agents/{id}/cmdb` | `internal/agentmgr/service.go:898` |
| GET | `/api/v1/agents/{id}/download-tasks` | `internal/agentmgr/service.go:906` |
| POST | `/api/v1/agents/{id}/download-tasks` | `internal/agentmgr/service.go:908` |
| POST | `/api/v1/agents/{id}/heartbeat` | `internal/agentmgr/service.go:879` |
| POST | `/api/v1/agents/{id}/merge` | `internal/agentmgr/service.go:921` |
| POST | `/api/v1/agents/{id}/restart` | `internal/agentmgr/service.go:891` |
| PUT | `/api/v1/agents/{id}/sandbox` | `internal/agentmgr/service.go:918` |
| GET | `/api/v1/agents/{id}/tasks` | `internal/agentmgr/service.go:887` |
| POST | `/api/v1/agents/{id}/tasks` | `internal/agentmgr/service.go:886` |
| POST | `/api/v1/agents/{id}/tasks/{task_id}` | `internal/agentmgr/service.go:888` |
| POST | `/api/v1/agents/{id}/uninstall` | `internal/agentmgr/service.go:897` |
| POST | `/api/v1/agents/{id}/upgrade` | `internal/agentmgr/service.go:892` |
| GET | `/api/v1/agents/{id}/whitelist` | `internal/agentmgr/service.go:915` |

## `/api/v1/agents/batch-execute` (1)

| Method | Path | 位置 |
| --- | --- | --- |
| POST | `/api/v1/agents/batch-execute` | `internal/agentmgr/service.go:923` |

## `/api/v1/agents/batch-install` (1)

| Method | Path | 位置 |
| --- | --- | --- |
| POST | `/api/v1/agents/batch-install` | `internal/agentmgr/service.go:880` |

## `/api/v1/agents/batch-upgrade` (1)

| Method | Path | 位置 |
| --- | --- | --- |
| POST | `/api/v1/agents/batch-upgrade` | `internal/agentmgr/service.go:896` |

## `/api/v1/agents/download` (1)

| Method | Path | 位置 |
| --- | --- | --- |
| GET | `/api/v1/agents/download` | `internal/agentmgr/service.go:883` |

## `/api/v1/agents/install-script` (1)

| Method | Path | 位置 |
| --- | --- | --- |
| GET | `/api/v1/agents/install-script` | `internal/agentmgr/service.go:882` |

## `/api/v1/agents/sandbox` (1)

| Method | Path | 位置 |
| --- | --- | --- |
| GET | `/api/v1/agents/sandbox` | `internal/agentmgr/service.go:917` |

## `/api/v1/agents/ssh-execute` (1)

| Method | Path | 位置 |
| --- | --- | --- |
| POST | `/api/v1/agents/ssh-execute` | `internal/agentmgr/service.go:925` |

## `/api/v1/agents/test-ssh` (1)

| Method | Path | 位置 |
| --- | --- | --- |
| POST | `/api/v1/agents/test-ssh` | `internal/agentmgr/service.go:881` |

## `/api/v1/agents/version-policy` (2)

| Method | Path | 位置 |
| --- | --- | --- |
| GET | `/api/v1/agents/version-policy` | `internal/agentmgr/service.go:894` |
| PUT | `/api/v1/agents/version-policy` | `internal/agentmgr/service.go:895` |

## `/api/v1/ai/agent-templates` (3)

| Method | Path | 位置 |
| --- | --- | --- |
| GET | `/api/v1/ai/agent-templates` | `internal/ai/service.go:142` |
| GET | `/api/v1/ai/agent-templates/{id}` | `internal/ai/service.go:143` |
| POST | `/api/v1/ai/agent-templates/{id}/instantiate` | `internal/ai/service.go:144` |

## `/api/v1/ai/agents` (5)

| Method | Path | 位置 |
| --- | --- | --- |
| GET | `/api/v1/ai/agents` | `internal/ai/service.go:108` |
| POST | `/api/v1/ai/agents` | `internal/ai/service.go:107` |
| DELETE | `/api/v1/ai/agents/{id}` | `internal/ai/service.go:111` |
| GET | `/api/v1/ai/agents/{id}` | `internal/ai/service.go:109` |
| PUT | `/api/v1/ai/agents/{id}` | `internal/ai/service.go:110` |

## `/api/v1/ai/approval-policy` (2)

| Method | Path | 位置 |
| --- | --- | --- |
| GET | `/api/v1/ai/approval-policy` | `internal/ai/service.go:223` |
| PUT | `/api/v1/ai/approval-policy` | `internal/ai/service.go:224` |

## `/api/v1/ai/approvals` (1)

| Method | Path | 位置 |
| --- | --- | --- |
| POST | `/api/v1/ai/approvals/{id}/respond` | `internal/ai/service.go:226` |

## `/api/v1/ai/approvals/pending` (1)

| Method | Path | 位置 |
| --- | --- | --- |
| GET | `/api/v1/ai/approvals/pending` | `internal/ai/service.go:225` |

## `/api/v1/ai/chat` (1)

| Method | Path | 位置 |
| --- | --- | --- |
| POST | `/api/v1/ai/chat` | `internal/ai/service.go:123` |

## `/api/v1/ai/conversations` (3)

| Method | Path | 位置 |
| --- | --- | --- |
| GET | `/api/v1/ai/conversations` | `internal/ai/service.go:125` |
| POST | `/api/v1/ai/conversations` | `internal/ai/service.go:127` |
| GET | `/api/v1/ai/conversations/{id}` | `internal/ai/service.go:126` |

## `/api/v1/ai/dataset` (1)

| Method | Path | 位置 |
| --- | --- | --- |
| GET | `/api/v1/ai/dataset` | `internal/ai/service.go:217` |

## `/api/v1/ai/eval/cases` (1)

| Method | Path | 位置 |
| --- | --- | --- |
| GET | `/api/v1/ai/eval/cases` | `internal/ai/service.go:131` |

## `/api/v1/ai/faultsim/injections` (3)

| Method | Path | 位置 |
| --- | --- | --- |
| GET | `/api/v1/ai/faultsim/injections` | `internal/ai/service.go:174` |
| POST | `/api/v1/ai/faultsim/injections` | `internal/ai/service.go:173` |
| POST | `/api/v1/ai/faultsim/injections/{id}/recover` | `internal/ai/service.go:175` |

## `/api/v1/ai/faultsim/scenarios` (1)

| Method | Path | 位置 |
| --- | --- | --- |
| GET | `/api/v1/ai/faultsim/scenarios` | `internal/ai/service.go:172` |

## `/api/v1/ai/feedback` (2)

| Method | Path | 位置 |
| --- | --- | --- |
| GET | `/api/v1/ai/feedback` | `internal/ai/service.go:129` |
| POST | `/api/v1/ai/feedback` | `internal/ai/service.go:128` |

## `/api/v1/ai/feedback/stats` (1)

| Method | Path | 位置 |
| --- | --- | --- |
| GET | `/api/v1/ai/feedback/stats` | `internal/ai/service.go:130` |

## `/api/v1/ai/finetune/tasks` (3)

| Method | Path | 位置 |
| --- | --- | --- |
| GET | `/api/v1/ai/finetune/tasks` | `internal/ai/service.go:168` |
| POST | `/api/v1/ai/finetune/tasks` | `internal/ai/service.go:167` |
| GET | `/api/v1/ai/finetune/tasks/{id}` | `internal/ai/service.go:169` |

## `/api/v1/ai/inspection-templates` (1)

| Method | Path | 位置 |
| --- | --- | --- |
| GET | `/api/v1/ai/inspection-templates` | `internal/ai/service.go:147` |

## `/api/v1/ai/inspections` (11)

| Method | Path | 位置 |
| --- | --- | --- |
| GET | `/api/v1/ai/inspections` | `internal/ai/service.go:148` |
| POST | `/api/v1/ai/inspections` | `internal/ai/service.go:149` |
| DELETE | `/api/v1/ai/inspections/{id}` | `internal/ai/service.go:152` |
| GET | `/api/v1/ai/inspections/{id}` | `internal/ai/service.go:150` |
| PUT | `/api/v1/ai/inspections/{id}` | `internal/ai/service.go:151` |
| POST | `/api/v1/ai/inspections/{id}/experiment` | `internal/ai/service.go:154` |
| POST | `/api/v1/ai/inspections/{id}/run` | `internal/ai/service.go:153` |
| GET | `/api/v1/ai/inspections/{id}/runs` | `internal/ai/service.go:155` |
| POST | `/api/v1/ai/inspections/{id}/runs/{runID}/escalate` | `internal/ai/service.go:156` |
| GET | `/api/v1/ai/inspections/{id}/runs/{runID}/remediate` | `internal/ai/service.go:179` |
| POST | `/api/v1/ai/inspections/{id}/runs/{runID}/remediate/generate` | `internal/ai/service.go:178` |

## `/api/v1/ai/memory` (3)

| Method | Path | 位置 |
| --- | --- | --- |
| GET | `/api/v1/ai/memory` | `internal/ai/service.go:183` |
| POST | `/api/v1/ai/memory` | `internal/ai/service.go:184` |
| DELETE | `/api/v1/ai/memory/{id}` | `internal/ai/service.go:185` |

## `/api/v1/ai/models` (1)

| Method | Path | 位置 |
| --- | --- | --- |
| GET | `/api/v1/ai/models` | `internal/ai/service.go:220` |

## `/api/v1/ai/plans` (2)

| Method | Path | 位置 |
| --- | --- | --- |
| GET | `/api/v1/ai/plans` | `internal/ai/service.go:120` |
| POST | `/api/v1/ai/plans` | `internal/ai/service.go:119` |

## `/api/v1/ai/prompts` (6)

| Method | Path | 位置 |
| --- | --- | --- |
| GET | `/api/v1/ai/prompts` | `internal/ai/service.go:204` |
| POST | `/api/v1/ai/prompts` | `internal/ai/service.go:205` |
| DELETE | `/api/v1/ai/prompts/{id}` | `internal/ai/service.go:208` |
| GET | `/api/v1/ai/prompts/{id}` | `internal/ai/service.go:206` |
| PUT | `/api/v1/ai/prompts/{id}` | `internal/ai/service.go:207` |
| POST | `/api/v1/ai/prompts/{id}/render` | `internal/ai/service.go:209` |

## `/api/v1/ai/rate-limit` (1)

| Method | Path | 位置 |
| --- | --- | --- |
| GET | `/api/v1/ai/rate-limit/{agent_id}` | `internal/ai/service.go:121` |

## `/api/v1/ai/remediate` (1)

| Method | Path | 位置 |
| --- | --- | --- |
| POST | `/api/v1/ai/remediate/{actionID}/execute` | `internal/ai/service.go:180` |

## `/api/v1/ai/scene-templates` (3)

| Method | Path | 位置 |
| --- | --- | --- |
| GET | `/api/v1/ai/scene-templates` | `internal/ai/service.go:159` |
| GET | `/api/v1/ai/scene-templates/{id}` | `internal/ai/service.go:160` |
| POST | `/api/v1/ai/scene-templates/{id}/instantiate` | `internal/ai/service.go:161` |

## `/api/v1/ai/settings` (1)

| Method | Path | 位置 |
| --- | --- | --- |
| POST | `/api/v1/ai/settings` | `internal/ai/service.go:124` |

## `/api/v1/ai/skills` (10)

| Method | Path | 位置 |
| --- | --- | --- |
| GET | `/api/v1/ai/skills` | `internal/ai/service.go:189` |
| DELETE | `/api/v1/ai/skills/{id}` | `internal/ai/service.go:191` |
| GET | `/api/v1/ai/skills/{id}` | `internal/ai/service.go:190` |
| GET | `/api/v1/ai/skills/{id}/export` | `internal/ai/service.go:193` |
| GET | `/api/v1/ai/skills/{id}/favorite` | `internal/ai/service.go:199` |
| POST | `/api/v1/ai/skills/{id}/favorite` | `internal/ai/service.go:198` |
| POST | `/api/v1/ai/skills/{id}/install` | `internal/ai/service.go:192` |
| POST | `/api/v1/ai/skills/{id}/rate` | `internal/ai/service.go:196` |
| GET | `/api/v1/ai/skills/{id}/rating` | `internal/ai/service.go:197` |
| GET | `/api/v1/ai/skills/{id}/versions` | `internal/ai/service.go:201` |

## `/api/v1/ai/skills/favorites` (1)

| Method | Path | 位置 |
| --- | --- | --- |
| GET | `/api/v1/ai/skills/favorites` | `internal/ai/service.go:200` |

## `/api/v1/ai/skills/import` (1)

| Method | Path | 位置 |
| --- | --- | --- |
| POST | `/api/v1/ai/skills/import` | `internal/ai/service.go:194` |

## `/api/v1/ai/skills/publish` (1)

| Method | Path | 位置 |
| --- | --- | --- |
| POST | `/api/v1/ai/skills/publish` | `internal/ai/service.go:188` |

## `/api/v1/ai/teams` (6)

| Method | Path | 位置 |
| --- | --- | --- |
| GET | `/api/v1/ai/teams` | `internal/ai/service.go:135` |
| POST | `/api/v1/ai/teams` | `internal/ai/service.go:134` |
| DELETE | `/api/v1/ai/teams/{id}` | `internal/ai/service.go:138` |
| GET | `/api/v1/ai/teams/{id}` | `internal/ai/service.go:136` |
| PUT | `/api/v1/ai/teams/{id}` | `internal/ai/service.go:137` |
| POST | `/api/v1/ai/teams/{id}/chat` | `internal/ai/service.go:139` |

## `/api/v1/ai/token-stats` (1)

| Method | Path | 位置 |
| --- | --- | --- |
| GET | `/api/v1/ai/token-stats` | `internal/ai/service.go:122` |

## `/api/v1/ai/tools` (2)

| Method | Path | 位置 |
| --- | --- | --- |
| GET | `/api/v1/ai/tools` | `internal/ai/service.go:112` |
| POST | `/api/v1/ai/tools/{name}/execute` | `internal/ai/service.go:113` |

## `/api/v1/ai/tools/custom` (4)

| Method | Path | 位置 |
| --- | --- | --- |
| GET | `/api/v1/ai/tools/custom` | `internal/ai/service.go:116` |
| POST | `/api/v1/ai/tools/custom` | `internal/ai/service.go:115` |
| DELETE | `/api/v1/ai/tools/custom/{id}` | `internal/ai/service.go:118` |
| PUT | `/api/v1/ai/tools/custom/{id}` | `internal/ai/service.go:117` |

## `/api/v1/ai/workforce-metrics` (1)

| Method | Path | 位置 |
| --- | --- | --- |
| GET | `/api/v1/ai/workforce-metrics` | `internal/ai/service.go:164` |

## `/api/v1/ai/workspace/generate` (1)

| Method | Path | 位置 |
| --- | --- | --- |
| POST | `/api/v1/ai/workspace/generate` | `internal/ai/service.go:213` |

## `/api/v1/ai/workspace/suggestions` (1)

| Method | Path | 位置 |
| --- | --- | --- |
| GET | `/api/v1/ai/workspace/suggestions` | `internal/ai/service.go:214` |

## `/api/v1/audit/events` (2)

| Method | Path | 位置 |
| --- | --- | --- |
| GET | `/api/v1/audit/events` | `internal/audit/service.go:54` |
| POST | `/api/v1/audit/events` | `internal/audit/service.go:53` |

## `/api/v1/audit/validate-query` (1)

| Method | Path | 位置 |
| --- | --- | --- |
| POST | `/api/v1/audit/validate-query` | `internal/audit/service.go:55` |

## `/api/v1/auth/login` (1)

| Method | Path | 位置 |
| --- | --- | --- |
| POST | `/api/v1/auth/login` | `internal/user/service.go:31` |

## `/api/v1/auth/logout` (1)

| Method | Path | 位置 |
| --- | --- | --- |
| POST | `/api/v1/auth/logout` | `internal/user/service.go:32` |

## `/api/v1/auth/refresh` (1)

| Method | Path | 位置 |
| --- | --- | --- |
| POST | `/api/v1/auth/refresh` | `internal/user/service.go:33` |

## `/api/v1/automation/inbound/channels` (4)

| Method | Path | 位置 |
| --- | --- | --- |
| GET | `/api/v1/automation/inbound/channels` | `internal/automation/inboundconfig/store.go:154` |
| POST | `/api/v1/automation/inbound/channels` | `internal/automation/inboundconfig/store.go:155` |
| DELETE | `/api/v1/automation/inbound/channels/{id}` | `internal/automation/inboundconfig/store.go:157` |
| PUT | `/api/v1/automation/inbound/channels/{id}` | `internal/automation/inboundconfig/store.go:156` |

## `/api/v1/cloud/credentials` (3)

| Method | Path | 位置 |
| --- | --- | --- |
| GET | `/api/v1/cloud/credentials` | `internal/cloud/service.go:91` |
| POST | `/api/v1/cloud/credentials` | `internal/cloud/service.go:92` |
| DELETE | `/api/v1/cloud/credentials/{id}` | `internal/cloud/service.go:93` |

## `/api/v1/cloud/providers` (1)

| Method | Path | 位置 |
| --- | --- | --- |
| GET | `/api/v1/cloud/providers` | `internal/cloud/service.go:90` |

## `/api/v1/cloud/resources/list` (1)

| Method | Path | 位置 |
| --- | --- | --- |
| POST | `/api/v1/cloud/resources/list` | `internal/cloud/service.go:94` |

## `/api/v1/cloud/resources/sync` (1)

| Method | Path | 位置 |
| --- | --- | --- |
| POST | `/api/v1/cloud/resources/sync` | `internal/cloud/service.go:95` |

## `/api/v1/cmdb/app-topology` (1)

| Method | Path | 位置 |
| --- | --- | --- |
| GET | `/api/v1/cmdb/app-topology` | `internal/cmdb/service.go:150` |

## `/api/v1/cmdb/change-requests` (4)

| Method | Path | 位置 |
| --- | --- | --- |
| GET | `/api/v1/cmdb/change-requests` | `internal/cmdb/changeassess/store.go:115` |
| POST | `/api/v1/cmdb/change-requests` | `internal/cmdb/changeassess/store.go:116` |
| DELETE | `/api/v1/cmdb/change-requests/{id}` | `internal/cmdb/changeassess/store.go:118` |
| PUT | `/api/v1/cmdb/change-requests/{id}` | `internal/cmdb/changeassess/store.go:117` |

## `/api/v1/cmdb/ci` (8)

| Method | Path | 位置 |
| --- | --- | --- |
| GET | `/api/v1/cmdb/ci` | `internal/cmdb/service.go:94` |
| POST | `/api/v1/cmdb/ci` | `internal/cmdb/service.go:97` |
| DELETE | `/api/v1/cmdb/ci/{id}` | `internal/cmdb/service.go:101` |
| GET | `/api/v1/cmdb/ci/{id}` | `internal/cmdb/service.go:99` |
| PUT | `/api/v1/cmdb/ci/{id}` | `internal/cmdb/service.go:100` |
| POST | `/api/v1/cmdb/ci/{id}/archive` | `internal/cmdb/service.go:102` |
| GET | `/api/v1/cmdb/ci/{id}/audit` | `internal/cmdb/service.go:104` |
| POST | `/api/v1/cmdb/ci/{id}/restore` | `internal/cmdb/service.go:103` |

## `/api/v1/cmdb/ci/batch` (1)

| Method | Path | 位置 |
| --- | --- | --- |
| POST | `/api/v1/cmdb/ci/batch` | `internal/cmdb/service.go:98` |

## `/api/v1/cmdb/ci/export` (1)

| Method | Path | 位置 |
| --- | --- | --- |
| GET | `/api/v1/cmdb/ci/export` | `internal/cmdb/service.go:95` |

## `/api/v1/cmdb/ci/import` (1)

| Method | Path | 位置 |
| --- | --- | --- |
| POST | `/api/v1/cmdb/ci/import` | `internal/cmdb/service.go:96` |

## `/api/v1/cmdb/collection-policy` (2)

| Method | Path | 位置 |
| --- | --- | --- |
| GET | `/api/v1/cmdb/collection-policy` | `internal/cmdb/service.go:182` |
| PUT | `/api/v1/cmdb/collection-policy` | `internal/cmdb/service.go:183` |

## `/api/v1/cmdb/collection-tasks` (5)

| Method | Path | 位置 |
| --- | --- | --- |
| GET | `/api/v1/cmdb/collection-tasks` | `internal/cmdb/service.go:184` |
| POST | `/api/v1/cmdb/collection-tasks` | `internal/cmdb/service.go:185` |
| DELETE | `/api/v1/cmdb/collection-tasks/{id}` | `internal/cmdb/service.go:187` |
| PUT | `/api/v1/cmdb/collection-tasks/{id}` | `internal/cmdb/service.go:186` |
| POST | `/api/v1/cmdb/collection-tasks/{id}/run` | `internal/cmdb/service.go:188` |

## `/api/v1/cmdb/collectors` (1)

| Method | Path | 位置 |
| --- | --- | --- |
| GET | `/api/v1/cmdb/collectors` | `internal/cmdb/service.go:121` |

## `/api/v1/cmdb/discovery` (8)

| Method | Path | 位置 |
| --- | --- | --- |
| GET | `/api/v1/cmdb/discovery` | `internal/cmdb/service.go:171` |
| POST | `/api/v1/cmdb/discovery` | `internal/cmdb/service.go:172` |
| DELETE | `/api/v1/cmdb/discovery/{id}` | `internal/cmdb/service.go:174` |
| PUT | `/api/v1/cmdb/discovery/{id}` | `internal/cmdb/service.go:173` |
| GET | `/api/v1/cmdb/discovery/{id}/results` | `internal/cmdb/service.go:177` |
| POST | `/api/v1/cmdb/discovery/{id}/run` | `internal/cmdb/service.go:176` |
| POST | `/api/v1/cmdb/discovery/{resultId}/sync` | `internal/cmdb/service.go:178` |
| POST | `/api/v1/cmdb/discovery/{taskId}/sync-all` | `internal/cmdb/service.go:179` |

## `/api/v1/cmdb/discovery/types` (1)

| Method | Path | 位置 |
| --- | --- | --- |
| GET | `/api/v1/cmdb/discovery/types` | `internal/cmdb/service.go:175` |

## `/api/v1/cmdb/field-rules` (3)

| Method | Path | 位置 |
| --- | --- | --- |
| GET | `/api/v1/cmdb/field-rules` | `internal/cmdb/service.go:153` |
| POST | `/api/v1/cmdb/field-rules` | `internal/cmdb/service.go:154` |
| DELETE | `/api/v1/cmdb/field-rules/{id}` | `internal/cmdb/service.go:155` |

## `/api/v1/cmdb/history` (2)

| Method | Path | 位置 |
| --- | --- | --- |
| GET | `/api/v1/cmdb/history` | `internal/cmdb/service.go:166` |
| GET | `/api/v1/cmdb/history/{ciId}` | `internal/cmdb/service.go:167` |

## `/api/v1/cmdb/history/detail` (1)

| Method | Path | 位置 |
| --- | --- | --- |
| GET | `/api/v1/cmdb/history/detail/{id}` | `internal/cmdb/service.go:168` |

## `/api/v1/cmdb/idc` (5)

| Method | Path | 位置 |
| --- | --- | --- |
| GET | `/api/v1/cmdb/idc` | `internal/cmdb/service.go:140` |
| POST | `/api/v1/cmdb/idc` | `internal/cmdb/service.go:141` |
| GET | `/api/v1/cmdb/idc/{idcId}/racks` | `internal/cmdb/service.go:143` |
| POST | `/api/v1/cmdb/idc/{idcId}/racks` | `internal/cmdb/service.go:144` |
| DELETE | `/api/v1/cmdb/idc/{id}` | `internal/cmdb/service.go:142` |

## `/api/v1/cmdb/impact` (1)

| Method | Path | 位置 |
| --- | --- | --- |
| GET | `/api/v1/cmdb/impact/{id}` | `internal/cmdb/service.go:114` |

## `/api/v1/cmdb/model-categories` (3)

| Method | Path | 位置 |
| --- | --- | --- |
| GET | `/api/v1/cmdb/model-categories` | `internal/cmdb/service.go:132` |
| POST | `/api/v1/cmdb/model-categories` | `internal/cmdb/service.go:133` |
| DELETE | `/api/v1/cmdb/model-categories/{id}` | `internal/cmdb/service.go:134` |

## `/api/v1/cmdb/model-type` (5)

| Method | Path | 位置 |
| --- | --- | --- |
| GET | `/api/v1/cmdb/model-type` | `internal/cmdb/service.go:124` |
| POST | `/api/v1/cmdb/model-type` | `internal/cmdb/service.go:123` |
| DELETE | `/api/v1/cmdb/model-type/{id}` | `internal/cmdb/service.go:127` |
| GET | `/api/v1/cmdb/model-type/{id}` | `internal/cmdb/service.go:125` |
| PUT | `/api/v1/cmdb/model-type/{id}` | `internal/cmdb/service.go:126` |

## `/api/v1/cmdb/model-type/export` (1)

| Method | Path | 位置 |
| --- | --- | --- |
| GET | `/api/v1/cmdb/model-type/export` | `internal/cmdb/service.go:128` |

## `/api/v1/cmdb/model-type/import` (1)

| Method | Path | 位置 |
| --- | --- | --- |
| POST | `/api/v1/cmdb/model-type/import` | `internal/cmdb/service.go:129` |

## `/api/v1/cmdb/onboard` (1)

| Method | Path | 位置 |
| --- | --- | --- |
| POST | `/api/v1/cmdb/onboard` | `internal/cmdb/service.go:189` |

## `/api/v1/cmdb/quality` (1)

| Method | Path | 位置 |
| --- | --- | --- |
| GET | `/api/v1/cmdb/quality` | `internal/cmdb/service.go:192` |

## `/api/v1/cmdb/racks` (3)

| Method | Path | 位置 |
| --- | --- | --- |
| DELETE | `/api/v1/cmdb/racks/{id}` | `internal/cmdb/service.go:145` |
| GET | `/api/v1/cmdb/racks/{id}/units` | `internal/cmdb/service.go:146` |
| POST | `/api/v1/cmdb/racks/{id}/units` | `internal/cmdb/service.go:147` |

## `/api/v1/cmdb/relation` (3)

| Method | Path | 位置 |
| --- | --- | --- |
| GET | `/api/v1/cmdb/relation` | `internal/cmdb/service.go:116` |
| POST | `/api/v1/cmdb/relation` | `internal/cmdb/service.go:117` |
| DELETE | `/api/v1/cmdb/relation/{id}` | `internal/cmdb/service.go:118` |

## `/api/v1/cmdb/saved-queries` (4)

| Method | Path | 位置 |
| --- | --- | --- |
| GET | `/api/v1/cmdb/saved-queries` | `internal/cmdb/service.go:108` |
| POST | `/api/v1/cmdb/saved-queries` | `internal/cmdb/service.go:109` |
| DELETE | `/api/v1/cmdb/saved-queries/{id}` | `internal/cmdb/service.go:111` |
| GET | `/api/v1/cmdb/saved-queries/{id}/resolve` | `internal/cmdb/service.go:110` |

## `/api/v1/cmdb/search` (1)

| Method | Path | 位置 |
| --- | --- | --- |
| GET | `/api/v1/cmdb/search` | `internal/cmdb/service.go:105` |

## `/api/v1/cmdb/stats` (1)

| Method | Path | 位置 |
| --- | --- | --- |
| GET | `/api/v1/cmdb/stats` | `internal/cmdb/service.go:137` |

## `/api/v1/cmdb/subscription-deliveries` (1)

| Method | Path | 位置 |
| --- | --- | --- |
| GET | `/api/v1/cmdb/subscription-deliveries` | `internal/cmdb/service.go:163` |

## `/api/v1/cmdb/subscriptions` (5)

| Method | Path | 位置 |
| --- | --- | --- |
| GET | `/api/v1/cmdb/subscriptions` | `internal/cmdb/service.go:158` |
| POST | `/api/v1/cmdb/subscriptions` | `internal/cmdb/service.go:159` |
| DELETE | `/api/v1/cmdb/subscriptions/{id}` | `internal/cmdb/service.go:161` |
| PUT | `/api/v1/cmdb/subscriptions/{id}` | `internal/cmdb/service.go:160` |
| POST | `/api/v1/cmdb/subscriptions/{id}/test` | `internal/cmdb/service.go:162` |

## `/api/v1/cmdb/topology` (1)

| Method | Path | 位置 |
| --- | --- | --- |
| GET | `/api/v1/cmdb/topology/{id}` | `internal/cmdb/service.go:113` |

## `/api/v1/collect-metrics` (1)

| Method | Path | 位置 |
| --- | --- | --- |
| GET | `/api/v1/collect-metrics` | `internal/agentmgr/service.go:903` |

## `/api/v1/collect-tasks` (5)

| Method | Path | 位置 |
| --- | --- | --- |
| GET | `/api/v1/collect-tasks` | `internal/agentmgr/service.go:901` |
| POST | `/api/v1/collect-tasks` | `internal/agentmgr/service.go:900` |
| DELETE | `/api/v1/collect-tasks/{id}` | `internal/agentmgr/service.go:905` |
| PUT | `/api/v1/collect-tasks/{id}` | `internal/agentmgr/service.go:904` |
| GET | `/api/v1/collect-tasks/{id}/runs` | `internal/agentmgr/service.go:902` |

## `/api/v1/deploy/apps` (4)

| Method | Path | 位置 |
| --- | --- | --- |
| GET | `/api/v1/deploy/apps` | `internal/deploy/service.go:36` |
| POST | `/api/v1/deploy/apps` | `internal/deploy/service.go:37` |
| DELETE | `/api/v1/deploy/apps/{id}` | `internal/deploy/service.go:39` |
| PUT | `/api/v1/deploy/apps/{id}` | `internal/deploy/service.go:38` |

## `/api/v1/deploy/environments` (4)

| Method | Path | 位置 |
| --- | --- | --- |
| GET | `/api/v1/deploy/environments` | `internal/deploy/service.go:50` |
| POST | `/api/v1/deploy/environments` | `internal/deploy/service.go:51` |
| DELETE | `/api/v1/deploy/environments/{id}` | `internal/deploy/service.go:53` |
| PUT | `/api/v1/deploy/environments/{id}` | `internal/deploy/service.go:52` |

## `/api/v1/deploy/history` (1)

| Method | Path | 位置 |
| --- | --- | --- |
| GET | `/api/v1/deploy/history` | `internal/deploy/service.go:55` |

## `/api/v1/deploy/releases` (8)

| Method | Path | 位置 |
| --- | --- | --- |
| GET | `/api/v1/deploy/releases` | `internal/deploy/service.go:41` |
| POST | `/api/v1/deploy/releases` | `internal/deploy/service.go:42` |
| DELETE | `/api/v1/deploy/releases/{id}` | `internal/deploy/service.go:44` |
| PUT | `/api/v1/deploy/releases/{id}` | `internal/deploy/service.go:43` |
| POST | `/api/v1/deploy/releases/{id}/approve` | `internal/deploy/service.go:45` |
| POST | `/api/v1/deploy/releases/{id}/deploy` | `internal/deploy/service.go:47` |
| POST | `/api/v1/deploy/releases/{id}/reject` | `internal/deploy/service.go:46` |
| POST | `/api/v1/deploy/releases/{id}/rollback` | `internal/deploy/service.go:48` |

## `/api/v1/files` (5)

| Method | Path | 位置 |
| --- | --- | --- |
| GET | `/api/v1/files` | `internal/agentmgr/service.go:911` |
| DELETE | `/api/v1/files/{file_id}` | `internal/agentmgr/service.go:912` |
| GET | `/api/v1/files/{file_id}` | `cmd/proxy/fileproxy.go:38` |
| GET | `/api/v1/files/{file_id}` | `internal/agentmgr/service.go:885` |
| GET | `/api/v1/files/{file_id}/meta` | `internal/agentmgr/service.go:913` |

## `/api/v1/files/upload` (1)

| Method | Path | 位置 |
| --- | --- | --- |
| POST | `/api/v1/files/upload` | `internal/agentmgr/service.go:910` |

## `/api/v1/iam/groups` (5)

| Method | Path | 位置 |
| --- | --- | --- |
| GET | `/api/v1/iam/groups` | `internal/iam/service.go:406` |
| POST | `/api/v1/iam/groups` | `internal/iam/service.go:407` |
| DELETE | `/api/v1/iam/groups/{id}` | `internal/iam/service.go:410` |
| GET | `/api/v1/iam/groups/{id}` | `internal/iam/service.go:408` |
| PUT | `/api/v1/iam/groups/{id}` | `internal/iam/service.go:409` |

## `/api/v1/iam/me/permissions` (1)

| Method | Path | 位置 |
| --- | --- | --- |
| GET | `/api/v1/iam/me/permissions` | `internal/iam/service.go:415` |

## `/api/v1/iam/permissions` (1)

| Method | Path | 位置 |
| --- | --- | --- |
| GET | `/api/v1/iam/permissions` | `internal/iam/service.go:398` |

## `/api/v1/iam/roles` (5)

| Method | Path | 位置 |
| --- | --- | --- |
| GET | `/api/v1/iam/roles` | `internal/iam/service.go:400` |
| POST | `/api/v1/iam/roles` | `internal/iam/service.go:401` |
| DELETE | `/api/v1/iam/roles/{id}` | `internal/iam/service.go:404` |
| GET | `/api/v1/iam/roles/{id}` | `internal/iam/service.go:402` |
| PUT | `/api/v1/iam/roles/{id}` | `internal/iam/service.go:403` |

## `/api/v1/iam/users` (2)

| Method | Path | 位置 |
| --- | --- | --- |
| GET | `/api/v1/iam/users/{user_id}/roles` | `internal/iam/service.go:412` |
| PUT | `/api/v1/iam/users/{user_id}/roles` | `internal/iam/service.go:413` |

## `/api/v1/inbound/dingtalk` (2)

| Method | Path | 位置 |
| --- | --- | --- |
| GET | `/api/v1/inbound/dingtalk` | `internal/automation/inboundbot/service.go:115` |
| POST | `/api/v1/inbound/dingtalk` | `internal/automation/inboundbot/service.go:114` |

## `/api/v1/inbound/feishu` (2)

| Method | Path | 位置 |
| --- | --- | --- |
| GET | `/api/v1/inbound/feishu` | `internal/automation/inboundbot/service.go:116` |
| POST | `/api/v1/inbound/feishu` | `internal/automation/inboundbot/service.go:117` |

## `/api/v1/inbound/wecom` (2)

| Method | Path | 位置 |
| --- | --- | --- |
| GET | `/api/v1/inbound/wecom` | `internal/automation/inboundbot/service.go:112` |
| POST | `/api/v1/inbound/wecom` | `internal/automation/inboundbot/service.go:113` |

## `/api/v1/itsm/catalog` (5)

| Method | Path | 位置 |
| --- | --- | --- |
| GET | `/api/v1/itsm/catalog` | `internal/itsm/catalog/catalog.go:340` |
| POST | `/api/v1/itsm/catalog` | `internal/itsm/catalog/catalog.go:342` |
| DELETE | `/api/v1/itsm/catalog/{id}` | `internal/itsm/catalog/catalog.go:344` |
| PUT | `/api/v1/itsm/catalog/{id}` | `internal/itsm/catalog/catalog.go:343` |
| POST | `/api/v1/itsm/catalog/{id}/fulfill` | `internal/itsm/service.go:309` |

## `/api/v1/itsm/catalog/reorder` (1)

| Method | Path | 位置 |
| --- | --- | --- |
| POST | `/api/v1/itsm/catalog/reorder` | `internal/itsm/catalog/catalog.go:346` |

## `/api/v1/itsm/catalog/usage` (1)

| Method | Path | 位置 |
| --- | --- | --- |
| GET | `/api/v1/itsm/catalog/usage` | `internal/itsm/catalog/catalog.go:345` |

## `/api/v1/itsm/catalog/visible` (1)

| Method | Path | 位置 |
| --- | --- | --- |
| GET | `/api/v1/itsm/catalog/visible` | `internal/itsm/catalog/catalog.go:341` |

## `/api/v1/itsm/changes` (5)

| Method | Path | 位置 |
| --- | --- | --- |
| GET | `/api/v1/itsm/changes` | `internal/itsm/service.go:343` |
| POST | `/api/v1/itsm/changes` | `internal/itsm/service.go:342` |
| GET | `/api/v1/itsm/changes/{id}` | `internal/itsm/service.go:344` |
| POST | `/api/v1/itsm/changes/{id}/execute` | `internal/itsm/service.go:346` |
| POST | `/api/v1/itsm/changes/{id}/vote` | `internal/itsm/service.go:345` |

## `/api/v1/itsm/form-dicts` (3)

| Method | Path | 位置 |
| --- | --- | --- |
| GET | `/api/v1/itsm/form-dicts` | `internal/itsm/service.go:336` |
| POST | `/api/v1/itsm/form-dicts` | `internal/itsm/service.go:337` |
| DELETE | `/api/v1/itsm/form-dicts/{id}` | `internal/itsm/service.go:338` |

## `/api/v1/itsm/form-options` (1)

| Method | Path | 位置 |
| --- | --- | --- |
| GET | `/api/v1/itsm/form-options` | `internal/itsm/service.go:339` |

## `/api/v1/itsm/forms` (2)

| Method | Path | 位置 |
| --- | --- | --- |
| GET | `/api/v1/itsm/forms` | `internal/itsm/service.go:333` |
| PUT | `/api/v1/itsm/forms` | `internal/itsm/service.go:334` |

## `/api/v1/itsm/ola/policies` (4)

| Method | Path | 位置 |
| --- | --- | --- |
| GET | `/api/v1/itsm/ola/policies` | `internal/itsm/ola/ola.go:121` |
| POST | `/api/v1/itsm/ola/policies` | `internal/itsm/ola/ola.go:122` |
| DELETE | `/api/v1/itsm/ola/policies/{id}` | `internal/itsm/ola/ola.go:124` |
| PUT | `/api/v1/itsm/ola/policies/{id}` | `internal/itsm/ola/ola.go:123` |

## `/api/v1/itsm/processes` (8)

| Method | Path | 位置 |
| --- | --- | --- |
| GET | `/api/v1/itsm/processes` | `internal/itsm/process/service.go:150` |
| POST | `/api/v1/itsm/processes` | `internal/itsm/process/service.go:151` |
| DELETE | `/api/v1/itsm/processes/{id}` | `internal/itsm/process/service.go:154` |
| GET | `/api/v1/itsm/processes/{id}` | `internal/itsm/process/service.go:152` |
| PUT | `/api/v1/itsm/processes/{id}` | `internal/itsm/process/service.go:153` |
| GET | `/api/v1/itsm/processes/{id}/bpmn` | `internal/itsm/process/service.go:155` |
| GET | `/api/v1/itsm/processes/{id}/versions` | `internal/itsm/process/service.go:157` |
| GET | `/api/v1/itsm/processes/{id}/versions/{version}` | `internal/itsm/process/service.go:158` |

## `/api/v1/itsm/processes/import-bpmn` (1)

| Method | Path | 位置 |
| --- | --- | --- |
| POST | `/api/v1/itsm/processes/import-bpmn` | `internal/itsm/process/service.go:156` |

## `/api/v1/itsm/sla/breached` (1)

| Method | Path | 位置 |
| --- | --- | --- |
| GET | `/api/v1/itsm/sla/breached` | `internal/itsm/service.go:330` |

## `/api/v1/itsm/sla/policies` (4)

| Method | Path | 位置 |
| --- | --- | --- |
| GET | `/api/v1/itsm/sla/policies` | `internal/itsm/sla/sla.go:179` |
| POST | `/api/v1/itsm/sla/policies` | `internal/itsm/sla/sla.go:180` |
| DELETE | `/api/v1/itsm/sla/policies/{id}` | `internal/itsm/sla/sla.go:182` |
| PUT | `/api/v1/itsm/sla/policies/{id}` | `internal/itsm/sla/sla.go:181` |

## `/api/v1/itsm/sla/stats` (1)

| Method | Path | 位置 |
| --- | --- | --- |
| GET | `/api/v1/itsm/sla/stats` | `internal/itsm/service.go:329` |

## `/api/v1/itsm/stats` (1)

| Method | Path | 位置 |
| --- | --- | --- |
| GET | `/api/v1/itsm/stats` | `internal/itsm/service.go:311` |

## `/api/v1/itsm/stats/export/csv` (1)

| Method | Path | 位置 |
| --- | --- | --- |
| GET | `/api/v1/itsm/stats/export/csv` | `internal/itsm/service.go:350` |

## `/api/v1/itsm/tickets` (36)

| Method | Path | 位置 |
| --- | --- | --- |
| GET | `/api/v1/itsm/tickets` | `internal/itsm/service.go:289` |
| POST | `/api/v1/itsm/tickets` | `internal/itsm/service.go:287` |
| GET | `/api/v1/itsm/tickets/{id}` | `internal/itsm/service.go:288` |
| PUT | `/api/v1/itsm/tickets/{id}` | `internal/itsm/service.go:307` |
| POST | `/api/v1/itsm/tickets/{id}/approve` | `internal/itsm/service.go:291` |
| POST | `/api/v1/itsm/tickets/{id}/assign` | `internal/itsm/service.go:296` |
| GET | `/api/v1/itsm/tickets/{id}/attachments` | `internal/itsm/service.go:325` |
| POST | `/api/v1/itsm/tickets/{id}/attachments` | `internal/itsm/service.go:324` |
| DELETE | `/api/v1/itsm/tickets/{id}/attachments/{aid}` | `internal/itsm/service.go:327` |
| GET | `/api/v1/itsm/tickets/{id}/attachments/{aid}` | `internal/itsm/service.go:326` |
| POST | `/api/v1/itsm/tickets/{id}/close` | `internal/itsm/service.go:297` |
| GET | `/api/v1/itsm/tickets/{id}/comments` | `internal/itsm/service.go:314` |
| POST | `/api/v1/itsm/tickets/{id}/comments` | `internal/itsm/service.go:313` |
| DELETE | `/api/v1/itsm/tickets/{id}/comments/{cid}` | `internal/itsm/service.go:316` |
| PUT | `/api/v1/itsm/tickets/{id}/comments/{cid}` | `internal/itsm/service.go:315` |
| POST | `/api/v1/itsm/tickets/{id}/complete` | `internal/itsm/service.go:294` |
| PATCH | `/api/v1/itsm/tickets/{id}/custom-fields` | `internal/itsm/service.go:340` |
| GET | `/api/v1/itsm/tickets/{id}/events` | `internal/itsm/service.go:310` |
| POST | `/api/v1/itsm/tickets/{id}/execute` | `internal/itsm/service.go:293` |
| POST | `/api/v1/itsm/tickets/{id}/hold` | `internal/itsm/service.go:303` |
| GET | `/api/v1/itsm/tickets/{id}/process` | `internal/itsm/service.go:290` |
| POST | `/api/v1/itsm/tickets/{id}/reject` | `internal/itsm/service.go:292` |
| POST | `/api/v1/itsm/tickets/{id}/relate` | `internal/itsm/service.go:306` |
| POST | `/api/v1/itsm/tickets/{id}/remind` | `internal/itsm/service.go:305` |
| POST | `/api/v1/itsm/tickets/{id}/reopen` | `internal/itsm/service.go:301` |
| POST | `/api/v1/itsm/tickets/{id}/resubmit` | `internal/itsm/service.go:302` |
| POST | `/api/v1/itsm/tickets/{id}/resume` | `internal/itsm/service.go:304` |
| POST | `/api/v1/itsm/tickets/{id}/rollback` | `internal/itsm/service.go:298` |
| GET | `/api/v1/itsm/tickets/{id}/sla` | `internal/itsm/service.go:331` |
| POST | `/api/v1/itsm/tickets/{id}/submit` | `internal/itsm/service.go:295` |
| GET | `/api/v1/itsm/tickets/{id}/subtasks` | `internal/itsm/service.go:319` |
| POST | `/api/v1/itsm/tickets/{id}/subtasks` | `internal/itsm/service.go:318` |
| DELETE | `/api/v1/itsm/tickets/{id}/subtasks/{sid}` | `internal/itsm/service.go:322` |
| GET | `/api/v1/itsm/tickets/{id}/subtasks/{sid}` | `internal/itsm/service.go:320` |
| PUT | `/api/v1/itsm/tickets/{id}/subtasks/{sid}` | `internal/itsm/service.go:321` |
| POST | `/api/v1/itsm/tickets/{id}/withdraw` | `internal/itsm/service.go:300` |

## `/api/v1/itsm/tickets/export/csv` (1)

| Method | Path | 位置 |
| --- | --- | --- |
| GET | `/api/v1/itsm/tickets/export/csv` | `internal/itsm/service.go:349` |

## `/api/v1/itsm/tickets/import` (1)

| Method | Path | 位置 |
| --- | --- | --- |
| POST | `/api/v1/itsm/tickets/import` | `internal/itsm/service.go:348` |

## `/api/v1/keys` (6)

| Method | Path | 位置 |
| --- | --- | --- |
| DELETE | `/api/v1/keys` | `internal/keystore/service.go:147` |
| GET | `/api/v1/keys` | `internal/keystore/service.go:143` |
| POST | `/api/v1/keys` | `internal/keystore/service.go:142` |
| DELETE | `/api/v1/keys/{id}` | `internal/keystore/service.go:146` |
| GET | `/api/v1/keys/{id}` | `internal/keystore/service.go:144` |
| PUT | `/api/v1/keys/{id}` | `internal/keystore/service.go:145` |

## `/api/v1/knowledge` (14)

| Method | Path | 位置 |
| --- | --- | --- |
| GET | `/api/v1/knowledge` | `internal/knowledge/service.go:248` |
| POST | `/api/v1/knowledge` | `internal/knowledge/service.go:247` |
| DELETE | `/api/v1/knowledge/{id}` | `internal/knowledge/service.go:251` |
| GET | `/api/v1/knowledge/{id}` | `internal/knowledge/service.go:249` |
| PUT | `/api/v1/knowledge/{id}` | `internal/knowledge/service.go:250` |
| POST | `/api/v1/knowledge/{id}/bookmark` | `internal/knowledge/social.go:133` |
| GET | `/api/v1/knowledge/{id}/comments` | `internal/knowledge/social.go:141` |
| POST | `/api/v1/knowledge/{id}/comments` | `internal/knowledge/social.go:140` |
| POST | `/api/v1/knowledge/{id}/follow` | `internal/knowledge/social.go:134` |
| POST | `/api/v1/knowledge/{id}/like` | `internal/knowledge/social.go:132` |
| POST | `/api/v1/knowledge/{id}/rate` | `internal/knowledge/social.go:139` |
| GET | `/api/v1/knowledge/{id}/stats` | `internal/knowledge/social.go:131` |
| GET | `/api/v1/knowledge/{id}/versions` | `internal/knowledge/service.go:258` |
| POST | `/api/v1/knowledge/{id}/versions/{version}/rollback` | `internal/knowledge/service.go:259` |

## `/api/v1/knowledge/bookmarks` (1)

| Method | Path | 位置 |
| --- | --- | --- |
| GET | `/api/v1/knowledge/bookmarks` | `internal/knowledge/social.go:136` |

## `/api/v1/knowledge/categories` (4)

| Method | Path | 位置 |
| --- | --- | --- |
| GET | `/api/v1/knowledge/categories` | `internal/knowledge/service.go:254` |
| POST | `/api/v1/knowledge/categories` | `internal/knowledge/service.go:255` |
| DELETE | `/api/v1/knowledge/categories/{id}` | `internal/knowledge/service.go:257` |
| PUT | `/api/v1/knowledge/categories/{id}` | `internal/knowledge/service.go:256` |

## `/api/v1/knowledge/follow-updates` (1)

| Method | Path | 位置 |
| --- | --- | --- |
| GET | `/api/v1/knowledge/follow-updates` | `internal/knowledge/social.go:137` |

## `/api/v1/knowledge/follow-updates/read` (1)

| Method | Path | 位置 |
| --- | --- | --- |
| POST | `/api/v1/knowledge/follow-updates/read` | `internal/knowledge/social.go:138` |

## `/api/v1/knowledge/follows` (1)

| Method | Path | 位置 |
| --- | --- | --- |
| GET | `/api/v1/knowledge/follows` | `internal/knowledge/social.go:135` |

## `/api/v1/knowledge/import` (1)

| Method | Path | 位置 |
| --- | --- | --- |
| POST | `/api/v1/knowledge/import` | `internal/knowledge/service.go:260` |

## `/api/v1/knowledge/import-file` (1)

| Method | Path | 位置 |
| --- | --- | --- |
| POST | `/api/v1/knowledge/import-file` | `internal/knowledge/service.go:262` |

## `/api/v1/knowledge/precipitation-rules` (5)

| Method | Path | 位置 |
| --- | --- | --- |
| GET | `/api/v1/knowledge/precipitation-rules` | `internal/knowledge/precipitation.go:132` |
| POST | `/api/v1/knowledge/precipitation-rules` | `internal/knowledge/precipitation.go:133` |
| DELETE | `/api/v1/knowledge/precipitation-rules/{id}` | `internal/knowledge/precipitation.go:135` |
| PUT | `/api/v1/knowledge/precipitation-rules/{id}` | `internal/knowledge/precipitation.go:134` |
| POST | `/api/v1/knowledge/precipitation-rules/{id}/run` | `internal/knowledge/precipitation.go:136` |

## `/api/v1/knowledge/reindex` (1)

| Method | Path | 位置 |
| --- | --- | --- |
| POST | `/api/v1/knowledge/reindex` | `internal/knowledge/service.go:252` |

## `/api/v1/lowcode/instances` (5)

| Method | Path | 位置 |
| --- | --- | --- |
| GET | `/api/v1/lowcode/instances` | `internal/lowcode/service/handler.go:34` |
| POST | `/api/v1/lowcode/instances` | `internal/lowcode/service/handler.go:33` |
| DELETE | `/api/v1/lowcode/instances/{id}` | `internal/lowcode/service/handler.go:37` |
| GET | `/api/v1/lowcode/instances/{id}` | `internal/lowcode/service/handler.go:35` |
| PUT | `/api/v1/lowcode/instances/{id}` | `internal/lowcode/service/handler.go:36` |

## `/api/v1/lowcode/models` (9)

| Method | Path | 位置 |
| --- | --- | --- |
| GET | `/api/v1/lowcode/models` | `internal/lowcode/service/handler.go:27` |
| POST | `/api/v1/lowcode/models` | `internal/lowcode/service/handler.go:26` |
| DELETE | `/api/v1/lowcode/models/{id}` | `internal/lowcode/service/handler.go:30` |
| GET | `/api/v1/lowcode/models/{id}` | `internal/lowcode/service/handler.go:28` |
| PUT | `/api/v1/lowcode/models/{id}` | `internal/lowcode/service/handler.go:29` |
| GET | `/api/v1/lowcode/models/{id}/versions` | `internal/lowcode/service/version_handler.go:29` |
| GET | `/api/v1/lowcode/models/{id}/versions/{versionId}` | `internal/lowcode/service/version_handler.go:30` |
| POST | `/api/v1/lowcode/models/{id}/versions/{versionId}/publish` | `internal/lowcode/service/version_handler.go:31` |
| POST | `/api/v1/lowcode/models/{id}/versions/{versionId}/rollback` | `internal/lowcode/service/version_handler.go:32` |

## `/api/v1/lowcode/pages` (9)

| Method | Path | 位置 |
| --- | --- | --- |
| GET | `/api/v1/lowcode/pages` | `internal/lowcode/service/handler.go:41` |
| POST | `/api/v1/lowcode/pages` | `internal/lowcode/service/handler.go:40` |
| DELETE | `/api/v1/lowcode/pages/{id}` | `internal/lowcode/service/handler.go:44` |
| GET | `/api/v1/lowcode/pages/{id}` | `internal/lowcode/service/handler.go:42` |
| PUT | `/api/v1/lowcode/pages/{id}` | `internal/lowcode/service/handler.go:43` |
| GET | `/api/v1/lowcode/pages/{id}/versions` | `internal/lowcode/service/version_handler.go:23` |
| GET | `/api/v1/lowcode/pages/{id}/versions/{versionId}` | `internal/lowcode/service/version_handler.go:24` |
| POST | `/api/v1/lowcode/pages/{id}/versions/{versionId}/publish` | `internal/lowcode/service/version_handler.go:25` |
| POST | `/api/v1/lowcode/pages/{id}/versions/{versionId}/rollback` | `internal/lowcode/service/version_handler.go:26` |

## `/api/v1/lowcode/sql` (1)

| Method | Path | 位置 |
| --- | --- | --- |
| GET | `/api/v1/lowcode/sql/{model_id}` | `internal/lowcode/service/handler.go:50` |

## `/api/v1/lowcode/validate` (1)

| Method | Path | 位置 |
| --- | --- | --- |
| POST | `/api/v1/lowcode/validate` | `internal/lowcode/service/handler.go:47` |

## `/api/v1/messages` (3)

| Method | Path | 位置 |
| --- | --- | --- |
| GET | `/api/v1/messages` | `internal/message/service.go:124` |
| DELETE | `/api/v1/messages/{id}` | `internal/message/service.go:128` |
| PUT | `/api/v1/messages/{id}/read` | `internal/message/service.go:127` |

## `/api/v1/messages/read-all` (1)

| Method | Path | 位置 |
| --- | --- | --- |
| PUT | `/api/v1/messages/read-all` | `internal/message/service.go:126` |

## `/api/v1/messages/unread-count` (1)

| Method | Path | 位置 |
| --- | --- | --- |
| GET | `/api/v1/messages/unread-count` | `internal/message/service.go:125` |

## `/api/v1/monitor/alert-stats` (1)

| Method | Path | 位置 |
| --- | --- | --- |
| GET | `/api/v1/monitor/alert-stats` | `internal/monitor/service.go:86` |

## `/api/v1/monitor/alerts` (3)

| Method | Path | 位置 |
| --- | --- | --- |
| GET | `/api/v1/monitor/alerts` | `internal/monitor/service.go:85` |
| POST | `/api/v1/monitor/alerts/{id}/ack` | `internal/monitor/service.go:90` |
| POST | `/api/v1/monitor/alerts/{id}/resolve` | `internal/monitor/service.go:91` |

## `/api/v1/monitor/alerts/rca` (1)

| Method | Path | 位置 |
| --- | --- | --- |
| GET | `/api/v1/monitor/alerts/rca` | `internal/monitor/service.go:87` |

## `/api/v1/monitor/alerts/rca/paths` (1)

| Method | Path | 位置 |
| --- | --- | --- |
| GET | `/api/v1/monitor/alerts/rca/paths` | `internal/monitor/service.go:88` |

## `/api/v1/monitor/metrics` (1)

| Method | Path | 位置 |
| --- | --- | --- |
| GET | `/api/v1/monitor/metrics` | `internal/monitor/service.go:95` |

## `/api/v1/monitor/plugins` (1)

| Method | Path | 位置 |
| --- | --- | --- |
| GET | `/api/v1/monitor/plugins` | `internal/monitor/service.go:96` |

## `/api/v1/monitor/predict` (1)

| Method | Path | 位置 |
| --- | --- | --- |
| POST | `/api/v1/monitor/predict` | `internal/monitor/service.go:89` |

## `/api/v1/monitor/probe` (1)

| Method | Path | 位置 |
| --- | --- | --- |
| POST | `/api/v1/monitor/probe` | `internal/monitor/service.go:84` |

## `/api/v1/monitor/rules` (3)

| Method | Path | 位置 |
| --- | --- | --- |
| GET | `/api/v1/monitor/rules` | `internal/monitor/service.go:92` |
| POST | `/api/v1/monitor/rules` | `internal/monitor/service.go:93` |
| DELETE | `/api/v1/monitor/rules/{id}` | `internal/monitor/service.go:94` |

## `/api/v1/monitor/targets` (4)

| Method | Path | 位置 |
| --- | --- | --- |
| GET | `/api/v1/monitor/targets` | `internal/monitor/service.go:80` |
| POST | `/api/v1/monitor/targets` | `internal/monitor/service.go:82` |
| DELETE | `/api/v1/monitor/targets/{id}` | `internal/monitor/service.go:83` |
| GET | `/api/v1/monitor/targets/{id}` | `internal/monitor/service.go:81` |

## `/api/v1/monitor/topology/parent` (1)

| Method | Path | 位置 |
| --- | --- | --- |
| POST | `/api/v1/monitor/topology/parent` | `internal/monitor/service.go:97` |

## `/api/v1/notification/channel-types` (1)

| Method | Path | 位置 |
| --- | --- | --- |
| GET | `/api/v1/notification/channel-types` | `internal/notification/service.go:256` |

## `/api/v1/notification/channels` (6)

| Method | Path | 位置 |
| --- | --- | --- |
| GET | `/api/v1/notification/channels` | `internal/notification/service.go:240` |
| POST | `/api/v1/notification/channels` | `internal/notification/service.go:241` |
| DELETE | `/api/v1/notification/channels/{id}` | `internal/notification/service.go:244` |
| GET | `/api/v1/notification/channels/{id}` | `internal/notification/service.go:242` |
| PUT | `/api/v1/notification/channels/{id}` | `internal/notification/service.go:243` |
| POST | `/api/v1/notification/channels/{id}/test` | `internal/notification/service.go:245` |

## `/api/v1/notification/events` (1)

| Method | Path | 位置 |
| --- | --- | --- |
| GET | `/api/v1/notification/events` | `internal/notification/service.go:255` |

## `/api/v1/notification/history` (1)

| Method | Path | 位置 |
| --- | --- | --- |
| GET | `/api/v1/notification/history` | `internal/notification/service.go:259` |

## `/api/v1/notification/policies` (5)

| Method | Path | 位置 |
| --- | --- | --- |
| GET | `/api/v1/notification/policies` | `internal/notification/service.go:248` |
| POST | `/api/v1/notification/policies` | `internal/notification/service.go:249` |
| DELETE | `/api/v1/notification/policies/{id}` | `internal/notification/service.go:252` |
| GET | `/api/v1/notification/policies/{id}` | `internal/notification/service.go:250` |
| PUT | `/api/v1/notification/policies/{id}` | `internal/notification/service.go:251` |

## `/api/v1/notification/send` (1)

| Method | Path | 位置 |
| --- | --- | --- |
| POST | `/api/v1/notification/send` | `internal/notification/service.go:262` |

## `/api/v1/oncall` (2)

| Method | Path | 位置 |
| --- | --- | --- |
| GET | `/api/v1/oncall` | `internal/itsm/oncall/service.go:182` |
| POST | `/api/v1/oncall` | `internal/itsm/oncall/service.go:183` |

## `/api/v1/oncall/rotations` (4)

| Method | Path | 位置 |
| --- | --- | --- |
| GET | `/api/v1/oncall/rotations` | `internal/itsm/oncall/service.go:184` |
| POST | `/api/v1/oncall/rotations` | `internal/itsm/oncall/service.go:185` |
| DELETE | `/api/v1/oncall/rotations/{id}` | `internal/itsm/oncall/service.go:186` |
| GET | `/api/v1/oncall/rotations/{id}/oncall` | `internal/itsm/oncall/service.go:187` |

## `/api/v1/ops/health` (1)

| Method | Path | 位置 |
| --- | --- | --- |
| GET | `/api/v1/ops/health` | `internal/ops/service.go:60` |

## `/api/v1/ops/restart` (1)

| Method | Path | 位置 |
| --- | --- | --- |
| POST | `/api/v1/ops/restart` | `internal/ops/service.go:63` |

## `/api/v1/ops/status` (1)

| Method | Path | 位置 |
| --- | --- | --- |
| GET | `/api/v1/ops/status` | `internal/ops/service.go:61` |

## `/api/v1/ops/stop` (1)

| Method | Path | 位置 |
| --- | --- | --- |
| POST | `/api/v1/ops/stop` | `internal/ops/service.go:62` |

## `/api/v1/proxies` (5)

| Method | Path | 位置 |
| --- | --- | --- |
| GET | `/api/v1/proxies` | `internal/agentmgr/service.go:927` |
| POST | `/api/v1/proxies` | `internal/agentmgr/service.go:928` |
| DELETE | `/api/v1/proxies/{id}` | `internal/agentmgr/service.go:931` |
| GET | `/api/v1/proxies/{id}` | `internal/agentmgr/service.go:929` |
| PUT | `/api/v1/proxies/{id}` | `internal/agentmgr/service.go:930` |

## `/api/v1/report/dashboards` (6)

| Method | Path | 位置 |
| --- | --- | --- |
| GET | `/api/v1/report/dashboards` | `internal/report/service.go:43` |
| POST | `/api/v1/report/dashboards` | `internal/report/service.go:44` |
| DELETE | `/api/v1/report/dashboards/{id}` | `internal/report/service.go:48` |
| GET | `/api/v1/report/dashboards/{id}` | `internal/report/service.go:45` |
| PUT | `/api/v1/report/dashboards/{id}` | `internal/report/service.go:47` |
| GET | `/api/v1/report/dashboards/{id}/render` | `internal/report/service.go:46` |

## `/api/v1/report/datasets` (1)

| Method | Path | 位置 |
| --- | --- | --- |
| GET | `/api/v1/report/datasets` | `internal/report/service.go:41` |

## `/api/v1/report/query` (1)

| Method | Path | 位置 |
| --- | --- | --- |
| POST | `/api/v1/report/query` | `internal/report/service.go:42` |

## `/api/v1/sandbox/exec` (1)

| Method | Path | 位置 |
| --- | --- | --- |
| POST | `/api/v1/sandbox/exec` | `internal/agentmgr/service.go:919` |

## `/api/v1/schedules` (5)

| Method | Path | 位置 |
| --- | --- | --- |
| GET | `/api/v1/schedules` | `internal/automation/scheduler/service.go:133` |
| POST | `/api/v1/schedules` | `internal/automation/scheduler/service.go:132` |
| DELETE | `/api/v1/schedules/{id}` | `internal/automation/scheduler/service.go:136` |
| GET | `/api/v1/schedules/{id}` | `internal/automation/scheduler/service.go:134` |
| PUT | `/api/v1/schedules/{id}` | `internal/automation/scheduler/service.go:135` |

## `/api/v1/scripts` (13)

| Method | Path | 位置 |
| --- | --- | --- |
| GET | `/api/v1/scripts` | `internal/automation/script/service.go:286` |
| POST | `/api/v1/scripts` | `internal/automation/script/service.go:285` |
| DELETE | `/api/v1/scripts/{id}` | `internal/automation/script/service.go:289` |
| GET | `/api/v1/scripts/{id}` | `internal/automation/script/service.go:287` |
| PUT | `/api/v1/scripts/{id}` | `internal/automation/script/service.go:288` |
| POST | `/api/v1/scripts/{id}/approve` | `internal/automation/script/service.go:297` |
| GET | `/api/v1/scripts/{id}/diff` | `internal/automation/script/service.go:294` |
| POST | `/api/v1/scripts/{id}/execute` | `internal/automation/script/service.go:290` |
| GET | `/api/v1/scripts/{id}/executions` | `internal/automation/script/service.go:291` |
| POST | `/api/v1/scripts/{id}/publish` | `internal/automation/script/service.go:296` |
| POST | `/api/v1/scripts/{id}/reject` | `internal/automation/script/service.go:298` |
| GET | `/api/v1/scripts/{id}/versions` | `internal/automation/script/service.go:292` |
| GET | `/api/v1/scripts/{id}/versions/{version}` | `internal/automation/script/service.go:293` |

## `/api/v1/scripts/check` (1)

| Method | Path | 位置 |
| --- | --- | --- |
| POST | `/api/v1/scripts/check` | `internal/automation/script/service.go:295` |

## `/api/v1/system/config` (2)

| Method | Path | 位置 |
| --- | --- | --- |
| GET | `/api/v1/system/config` | `internal/sysconfig/service.go:63` |
| PUT | `/api/v1/system/config` | `internal/sysconfig/service.go:64` |

## `/api/v1/system/config/categories` (1)

| Method | Path | 位置 |
| --- | --- | --- |
| GET | `/api/v1/system/config/categories` | `internal/sysconfig/service.go:65` |

## `/api/v1/users` (6)

| Method | Path | 位置 |
| --- | --- | --- |
| GET | `/api/v1/users` | `internal/user/service.go:34` |
| POST | `/api/v1/users` | `internal/user/service.go:35` |
| DELETE | `/api/v1/users/{id}` | `internal/user/service.go:38` |
| GET | `/api/v1/users/{id}` | `internal/user/service.go:36` |
| PUT | `/api/v1/users/{id}` | `internal/user/service.go:37` |
| PUT | `/api/v1/users/{id}/password` | `internal/user/service.go:39` |

## `/api/v1/users/me` (1)

| Method | Path | 位置 |
| --- | --- | --- |
| GET | `/api/v1/users/me` | `internal/user/service.go:40` |

## `/api/v1/vuln/scans` (5)

| Method | Path | 位置 |
| --- | --- | --- |
| GET | `/api/v1/vuln/scans` | `internal/vuln/service.go:71` |
| POST | `/api/v1/vuln/scans` | `internal/vuln/service.go:70` |
| DELETE | `/api/v1/vuln/scans/{id}` | `internal/vuln/service.go:73` |
| GET | `/api/v1/vuln/scans/{id}` | `internal/vuln/service.go:72` |
| GET | `/api/v1/vuln/scans/{id}/findings` | `internal/vuln/service.go:74` |

## `/api/v1/vuln/scans/export/csv` (1)

| Method | Path | 位置 |
| --- | --- | --- |
| GET | `/api/v1/vuln/scans/export/csv` | `internal/vuln/service.go:76` |

## `/api/v1/vuln/stats` (1)

| Method | Path | 位置 |
| --- | --- | --- |
| GET | `/api/v1/vuln/stats` | `internal/vuln/service.go:75` |

## `/api/v1/workflows` (7)

| Method | Path | 位置 |
| --- | --- | --- |
| GET | `/api/v1/workflows` | `internal/automation/workflow/service.go:247` |
| POST | `/api/v1/workflows` | `internal/automation/workflow/service.go:246` |
| DELETE | `/api/v1/workflows/{id}` | `internal/automation/workflow/service.go:250` |
| GET | `/api/v1/workflows/{id}` | `internal/automation/workflow/service.go:248` |
| PUT | `/api/v1/workflows/{id}` | `internal/automation/workflow/service.go:249` |
| POST | `/api/v1/workflows/{id}/execute` | `internal/automation/workflow/service.go:251` |
| GET | `/api/v1/workflows/{id}/executions` | `internal/automation/workflow/service.go:252` |

## `/api/v1/workflows/approvals` (3)

| Method | Path | 位置 |
| --- | --- | --- |
| GET | `/api/v1/workflows/approvals` | `internal/automation/workflow/service.go:254` |
| POST | `/api/v1/workflows/approvals/{id}/approve` | `internal/automation/workflow/service.go:255` |
| POST | `/api/v1/workflows/approvals/{id}/reject` | `internal/automation/workflow/service.go:256` |

## `/api/v1/workflows/executions` (1)

| Method | Path | 位置 |
| --- | --- | --- |
| POST | `/api/v1/workflows/executions/{exec_id}/cancel` | `internal/automation/workflow/service.go:257` |

## `/api/v1/workflows/templates/list` (1)

| Method | Path | 位置 |
| --- | --- | --- |
| GET | `/api/v1/workflows/templates/list` | `internal/automation/workflow/service.go:259` |

## `/swagger` (1)

| Method | Path | 位置 |
| --- | --- | --- |
| GET | `/swagger/doc.json` | `cmd/core/server.go:157` |

