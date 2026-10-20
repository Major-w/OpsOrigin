# OpsOrigin API 路由清单

> 由 `tools/routes` 自动生成(`make routes` 重新生成), 共 **634** 个端点。
> 方法与路径为 Go 1.22 ServeMux 注册形态。

## `/api/v1/agent-tasks` (1)

| Method | Path | 位置 |
| --- | --- | --- |
| GET | `/api/v1/agent-tasks/{task_id}` | `internal/agentmgr/service.go:838` |

## `/api/v1/agents` (22)

| Method | Path | 位置 |
| --- | --- | --- |
| GET | `/api/v1/agents` | `internal/agentmgr/service.go:787` |
| POST | `/api/v1/agents` | `internal/agentmgr/service.go:786` |
| DELETE | `/api/v1/agents/{id}` | `internal/agentmgr/service.go:790` |
| GET | `/api/v1/agents/{id}` | `internal/agentmgr/service.go:788` |
| PUT | `/api/v1/agents/{id}` | `internal/agentmgr/service.go:789` |
| GET | `/api/v1/agents/{id}/circuit-breaker` | `internal/agentmgr/service.go:801` |
| PUT | `/api/v1/agents/{id}/circuit-breaker` | `internal/agentmgr/service.go:802` |
| POST | `/api/v1/agents/{id}/cmdb` | `internal/agentmgr/service.go:812` |
| GET | `/api/v1/agents/{id}/download-tasks` | `internal/agentmgr/service.go:820` |
| POST | `/api/v1/agents/{id}/download-tasks` | `internal/agentmgr/service.go:822` |
| POST | `/api/v1/agents/{id}/heartbeat` | `internal/agentmgr/service.go:791` |
| POST | `/api/v1/agents/{id}/merge` | `internal/agentmgr/service.go:835` |
| GET | `/api/v1/agents/{id}/resource-guard` | `internal/agentmgr/service.go:803` |
| PUT | `/api/v1/agents/{id}/resource-guard` | `internal/agentmgr/service.go:804` |
| POST | `/api/v1/agents/{id}/restart` | `internal/agentmgr/service.go:805` |
| PUT | `/api/v1/agents/{id}/sandbox` | `internal/agentmgr/service.go:832` |
| GET | `/api/v1/agents/{id}/tasks` | `internal/agentmgr/service.go:799` |
| POST | `/api/v1/agents/{id}/tasks` | `internal/agentmgr/service.go:798` |
| POST | `/api/v1/agents/{id}/tasks/{task_id}` | `internal/agentmgr/service.go:800` |
| POST | `/api/v1/agents/{id}/uninstall` | `internal/agentmgr/service.go:811` |
| POST | `/api/v1/agents/{id}/upgrade` | `internal/agentmgr/service.go:806` |
| GET | `/api/v1/agents/{id}/whitelist` | `internal/agentmgr/service.go:829` |

## `/api/v1/agents/batch-execute` (1)

| Method | Path | 位置 |
| --- | --- | --- |
| POST | `/api/v1/agents/batch-execute` | `internal/agentmgr/service.go:837` |

## `/api/v1/agents/batch-install` (1)

| Method | Path | 位置 |
| --- | --- | --- |
| POST | `/api/v1/agents/batch-install` | `internal/agentmgr/service.go:792` |

## `/api/v1/agents/batch-upgrade` (1)

| Method | Path | 位置 |
| --- | --- | --- |
| POST | `/api/v1/agents/batch-upgrade` | `internal/agentmgr/service.go:810` |

## `/api/v1/agents/download` (1)

| Method | Path | 位置 |
| --- | --- | --- |
| GET | `/api/v1/agents/download` | `internal/agentmgr/service.go:795` |

## `/api/v1/agents/install-script` (1)

| Method | Path | 位置 |
| --- | --- | --- |
| GET | `/api/v1/agents/install-script` | `internal/agentmgr/service.go:794` |

## `/api/v1/agents/sandbox` (1)

| Method | Path | 位置 |
| --- | --- | --- |
| GET | `/api/v1/agents/sandbox` | `internal/agentmgr/service.go:831` |

## `/api/v1/agents/ssh-execute` (1)

| Method | Path | 位置 |
| --- | --- | --- |
| POST | `/api/v1/agents/ssh-execute` | `internal/agentmgr/service.go:839` |

## `/api/v1/agents/test-ssh` (1)

| Method | Path | 位置 |
| --- | --- | --- |
| POST | `/api/v1/agents/test-ssh` | `internal/agentmgr/service.go:793` |

## `/api/v1/agents/version-policy` (2)

| Method | Path | 位置 |
| --- | --- | --- |
| GET | `/api/v1/agents/version-policy` | `internal/agentmgr/service.go:808` |
| PUT | `/api/v1/agents/version-policy` | `internal/agentmgr/service.go:809` |

## `/api/v1/ai/agent-templates` (3)

| Method | Path | 位置 |
| --- | --- | --- |
| GET | `/api/v1/ai/agent-templates` | `internal/ai/service.go:304` |
| GET | `/api/v1/ai/agent-templates/{id}` | `internal/ai/service.go:305` |
| POST | `/api/v1/ai/agent-templates/{id}/instantiate` | `internal/ai/service.go:306` |

## `/api/v1/ai/agents` (5)

| Method | Path | 位置 |
| --- | --- | --- |
| GET | `/api/v1/ai/agents` | `internal/ai/service.go:230` |
| POST | `/api/v1/ai/agents` | `internal/ai/service.go:229` |
| DELETE | `/api/v1/ai/agents/{id}` | `internal/ai/service.go:233` |
| GET | `/api/v1/ai/agents/{id}` | `internal/ai/service.go:231` |
| PUT | `/api/v1/ai/agents/{id}` | `internal/ai/service.go:232` |

## `/api/v1/ai/alerts/triage` (1)

| Method | Path | 位置 |
| --- | --- | --- |
| POST | `/api/v1/ai/alerts/triage` | `internal/ai/service.go:354` |

## `/api/v1/ai/approval-policy` (2)

| Method | Path | 位置 |
| --- | --- | --- |
| GET | `/api/v1/ai/approval-policy` | `internal/ai/service.go:407` |
| PUT | `/api/v1/ai/approval-policy` | `internal/ai/service.go:408` |

## `/api/v1/ai/approvals` (1)

| Method | Path | 位置 |
| --- | --- | --- |
| POST | `/api/v1/ai/approvals/{id}/respond` | `internal/ai/service.go:410` |

## `/api/v1/ai/approvals/pending` (1)

| Method | Path | 位置 |
| --- | --- | --- |
| GET | `/api/v1/ai/approvals/pending` | `internal/ai/service.go:409` |

## `/api/v1/ai/attachments` (2)

| Method | Path | 位置 |
| --- | --- | --- |
| POST | `/api/v1/ai/attachments` | `internal/ai/service.go:277` |
| GET | `/api/v1/ai/attachments/{id}` | `internal/ai/service.go:278` |

## `/api/v1/ai/autonomy` (2)

| Method | Path | 位置 |
| --- | --- | --- |
| GET | `/api/v1/ai/autonomy` | `internal/ai/service.go:350` |
| PUT | `/api/v1/ai/autonomy` | `internal/ai/service.go:351` |

## `/api/v1/ai/chat` (1)

| Method | Path | 位置 |
| --- | --- | --- |
| POST | `/api/v1/ai/chat` | `internal/ai/service.go:246` |

## `/api/v1/ai/conversations` (15)

| Method | Path | 位置 |
| --- | --- | --- |
| GET | `/api/v1/ai/conversations` | `internal/ai/service.go:249` |
| POST | `/api/v1/ai/conversations` | `internal/ai/service.go:251` |
| DELETE | `/api/v1/ai/conversations/{id}` | `internal/ai/service.go:252` |
| GET | `/api/v1/ai/conversations/{id}` | `internal/ai/service.go:250` |
| PATCH | `/api/v1/ai/conversations/{id}` | `internal/ai/service.go:253` |
| GET | `/api/v1/ai/conversations/{id}/artifacts` | `internal/ai/service.go:269` |
| POST | `/api/v1/ai/conversations/{id}/artifacts` | `internal/ai/service.go:268` |
| DELETE | `/api/v1/ai/conversations/{id}/artifacts/{aid}` | `internal/ai/service.go:271` |
| GET | `/api/v1/ai/conversations/{id}/artifacts/{aid}` | `internal/ai/service.go:270` |
| GET | `/api/v1/ai/conversations/{id}/shares` | `internal/ai/service.go:256` |
| POST | `/api/v1/ai/conversations/{id}/shares` | `internal/ai/service.go:255` |
| DELETE | `/api/v1/ai/conversations/{id}/shares/{sid}` | `internal/ai/service.go:257` |
| GET | `/api/v1/ai/conversations/{id}/workspace` | `internal/ai/service.go:280` |
| DELETE | `/api/v1/ai/conversations/{id}/workspace/{fid}` | `internal/ai/service.go:282` |
| GET | `/api/v1/ai/conversations/{id}/workspace/{fid}` | `internal/ai/service.go:281` |

## `/api/v1/ai/dataset` (1)

| Method | Path | 位置 |
| --- | --- | --- |
| GET | `/api/v1/ai/dataset` | `internal/ai/service.go:401` |

## `/api/v1/ai/eval/cases` (1)

| Method | Path | 位置 |
| --- | --- | --- |
| GET | `/api/v1/ai/eval/cases` | `internal/ai/service.go:290` |

## `/api/v1/ai/evolution/fetch` (1)

| Method | Path | 位置 |
| --- | --- | --- |
| POST | `/api/v1/ai/evolution/fetch` | `internal/ai/service.go:266` |

## `/api/v1/ai/evolution/policy` (2)

| Method | Path | 位置 |
| --- | --- | --- |
| GET | `/api/v1/ai/evolution/policy` | `internal/ai/service.go:260` |
| PUT | `/api/v1/ai/evolution/policy` | `internal/ai/service.go:261` |

## `/api/v1/ai/evolution/sources` (4)

| Method | Path | 位置 |
| --- | --- | --- |
| GET | `/api/v1/ai/evolution/sources` | `internal/ai/service.go:262` |
| POST | `/api/v1/ai/evolution/sources` | `internal/ai/service.go:263` |
| DELETE | `/api/v1/ai/evolution/sources/{id}` | `internal/ai/service.go:265` |
| PUT | `/api/v1/ai/evolution/sources/{id}` | `internal/ai/service.go:264` |

## `/api/v1/ai/evolution/stats` (1)

| Method | Path | 位置 |
| --- | --- | --- |
| GET | `/api/v1/ai/evolution/stats` | `internal/ai/service.go:258` |

## `/api/v1/ai/export` (1)

| Method | Path | 位置 |
| --- | --- | --- |
| POST | `/api/v1/ai/export` | `internal/ai/service.go:273` |

## `/api/v1/ai/faultsim/injections` (3)

| Method | Path | 位置 |
| --- | --- | --- |
| GET | `/api/v1/ai/faultsim/injections` | `internal/ai/service.go:336` |
| POST | `/api/v1/ai/faultsim/injections` | `internal/ai/service.go:335` |
| POST | `/api/v1/ai/faultsim/injections/{id}/recover` | `internal/ai/service.go:337` |

## `/api/v1/ai/faultsim/scenarios` (1)

| Method | Path | 位置 |
| --- | --- | --- |
| GET | `/api/v1/ai/faultsim/scenarios` | `internal/ai/service.go:334` |

## `/api/v1/ai/feedback` (2)

| Method | Path | 位置 |
| --- | --- | --- |
| GET | `/api/v1/ai/feedback` | `internal/ai/service.go:288` |
| POST | `/api/v1/ai/feedback` | `internal/ai/service.go:287` |

## `/api/v1/ai/feedback/stats` (1)

| Method | Path | 位置 |
| --- | --- | --- |
| GET | `/api/v1/ai/feedback/stats` | `internal/ai/service.go:289` |

## `/api/v1/ai/finetune/tasks` (3)

| Method | Path | 位置 |
| --- | --- | --- |
| GET | `/api/v1/ai/finetune/tasks` | `internal/ai/service.go:330` |
| POST | `/api/v1/ai/finetune/tasks` | `internal/ai/service.go:329` |
| GET | `/api/v1/ai/finetune/tasks/{id}` | `internal/ai/service.go:331` |

## `/api/v1/ai/inspection-templates` (1)

| Method | Path | 位置 |
| --- | --- | --- |
| GET | `/api/v1/ai/inspection-templates` | `internal/ai/service.go:309` |

## `/api/v1/ai/inspections` (11)

| Method | Path | 位置 |
| --- | --- | --- |
| GET | `/api/v1/ai/inspections` | `internal/ai/service.go:310` |
| POST | `/api/v1/ai/inspections` | `internal/ai/service.go:311` |
| DELETE | `/api/v1/ai/inspections/{id}` | `internal/ai/service.go:314` |
| GET | `/api/v1/ai/inspections/{id}` | `internal/ai/service.go:312` |
| PUT | `/api/v1/ai/inspections/{id}` | `internal/ai/service.go:313` |
| POST | `/api/v1/ai/inspections/{id}/experiment` | `internal/ai/service.go:316` |
| POST | `/api/v1/ai/inspections/{id}/run` | `internal/ai/service.go:315` |
| GET | `/api/v1/ai/inspections/{id}/runs` | `internal/ai/service.go:317` |
| POST | `/api/v1/ai/inspections/{id}/runs/{runID}/escalate` | `internal/ai/service.go:318` |
| GET | `/api/v1/ai/inspections/{id}/runs/{runID}/remediate` | `internal/ai/service.go:341` |
| POST | `/api/v1/ai/inspections/{id}/runs/{runID}/remediate/generate` | `internal/ai/service.go:340` |

## `/api/v1/ai/llm-presets` (1)

| Method | Path | 位置 |
| --- | --- | --- |
| GET | `/api/v1/ai/llm-presets` | `internal/ai/service.go:248` |

## `/api/v1/ai/mcp/servers` (1)

| Method | Path | 位置 |
| --- | --- | --- |
| GET | `/api/v1/ai/mcp/servers` | `internal/ai/service.go:235` |

## `/api/v1/ai/memories` (3)

| Method | Path | 位置 |
| --- | --- | --- |
| GET | `/api/v1/ai/memories` | `internal/ai/service.go:284` |
| POST | `/api/v1/ai/memories` | `internal/ai/service.go:285` |
| DELETE | `/api/v1/ai/memories/{id}` | `internal/ai/service.go:286` |

## `/api/v1/ai/memory` (3)

| Method | Path | 位置 |
| --- | --- | --- |
| GET | `/api/v1/ai/memory` | `internal/ai/service.go:367` |
| POST | `/api/v1/ai/memory` | `internal/ai/service.go:368` |
| DELETE | `/api/v1/ai/memory/{id}` | `internal/ai/service.go:369` |

## `/api/v1/ai/models` (1)

| Method | Path | 位置 |
| --- | --- | --- |
| GET | `/api/v1/ai/models` | `internal/ai/service.go:404` |

## `/api/v1/ai/plans` (2)

| Method | Path | 位置 |
| --- | --- | --- |
| GET | `/api/v1/ai/plans` | `internal/ai/service.go:243` |
| POST | `/api/v1/ai/plans` | `internal/ai/service.go:242` |

## `/api/v1/ai/postmortems` (3)

| Method | Path | 位置 |
| --- | --- | --- |
| GET | `/api/v1/ai/postmortems` | `internal/ai/service.go:363` |
| POST | `/api/v1/ai/postmortems` | `internal/ai/service.go:362` |
| POST | `/api/v1/ai/postmortems/{id}/publish` | `internal/ai/service.go:364` |

## `/api/v1/ai/presentation` (1)

| Method | Path | 位置 |
| --- | --- | --- |
| POST | `/api/v1/ai/presentation` | `internal/ai/service.go:275` |

## `/api/v1/ai/prompts` (6)

| Method | Path | 位置 |
| --- | --- | --- |
| GET | `/api/v1/ai/prompts` | `internal/ai/service.go:388` |
| POST | `/api/v1/ai/prompts` | `internal/ai/service.go:389` |
| DELETE | `/api/v1/ai/prompts/{id}` | `internal/ai/service.go:392` |
| GET | `/api/v1/ai/prompts/{id}` | `internal/ai/service.go:390` |
| PUT | `/api/v1/ai/prompts/{id}` | `internal/ai/service.go:391` |
| POST | `/api/v1/ai/prompts/{id}/render` | `internal/ai/service.go:393` |

## `/api/v1/ai/rate-limit` (1)

| Method | Path | 位置 |
| --- | --- | --- |
| GET | `/api/v1/ai/rate-limit/{agent_id}` | `internal/ai/service.go:244` |

## `/api/v1/ai/rca-records` (3)

| Method | Path | 位置 |
| --- | --- | --- |
| GET | `/api/v1/ai/rca-records` | `internal/ai/service.go:358` |
| POST | `/api/v1/ai/rca-records` | `internal/ai/service.go:357` |
| POST | `/api/v1/ai/rca-records/{id}/label` | `internal/ai/service.go:359` |

## `/api/v1/ai/remediate` (1)

| Method | Path | 位置 |
| --- | --- | --- |
| POST | `/api/v1/ai/remediate/{actionID}/execute` | `internal/ai/service.go:342` |

## `/api/v1/ai/runbooks` (3)

| Method | Path | 位置 |
| --- | --- | --- |
| GET | `/api/v1/ai/runbooks` | `internal/ai/service.go:345` |
| GET | `/api/v1/ai/runbooks/{id}` | `internal/ai/service.go:346` |
| POST | `/api/v1/ai/runbooks/{id}/execute` | `internal/ai/service.go:347` |

## `/api/v1/ai/scene-templates` (3)

| Method | Path | 位置 |
| --- | --- | --- |
| GET | `/api/v1/ai/scene-templates` | `internal/ai/service.go:321` |
| GET | `/api/v1/ai/scene-templates/{id}` | `internal/ai/service.go:322` |
| POST | `/api/v1/ai/scene-templates/{id}/instantiate` | `internal/ai/service.go:323` |

## `/api/v1/ai/settings` (1)

| Method | Path | 位置 |
| --- | --- | --- |
| POST | `/api/v1/ai/settings` | `internal/ai/service.go:247` |

## `/api/v1/ai/skills` (10)

| Method | Path | 位置 |
| --- | --- | --- |
| GET | `/api/v1/ai/skills` | `internal/ai/service.go:373` |
| DELETE | `/api/v1/ai/skills/{id}` | `internal/ai/service.go:375` |
| GET | `/api/v1/ai/skills/{id}` | `internal/ai/service.go:374` |
| GET | `/api/v1/ai/skills/{id}/export` | `internal/ai/service.go:377` |
| GET | `/api/v1/ai/skills/{id}/favorite` | `internal/ai/service.go:383` |
| POST | `/api/v1/ai/skills/{id}/favorite` | `internal/ai/service.go:382` |
| POST | `/api/v1/ai/skills/{id}/install` | `internal/ai/service.go:376` |
| POST | `/api/v1/ai/skills/{id}/rate` | `internal/ai/service.go:380` |
| GET | `/api/v1/ai/skills/{id}/rating` | `internal/ai/service.go:381` |
| GET | `/api/v1/ai/skills/{id}/versions` | `internal/ai/service.go:385` |

## `/api/v1/ai/skills/favorites` (1)

| Method | Path | 位置 |
| --- | --- | --- |
| GET | `/api/v1/ai/skills/favorites` | `internal/ai/service.go:384` |

## `/api/v1/ai/skills/import` (1)

| Method | Path | 位置 |
| --- | --- | --- |
| POST | `/api/v1/ai/skills/import` | `internal/ai/service.go:378` |

## `/api/v1/ai/skills/publish` (1)

| Method | Path | 位置 |
| --- | --- | --- |
| POST | `/api/v1/ai/skills/publish` | `internal/ai/service.go:372` |

## `/api/v1/ai/tasks` (15)

| Method | Path | 位置 |
| --- | --- | --- |
| GET | `/api/v1/ai/tasks` | `internal/ai/tasks.go:895` |
| POST | `/api/v1/ai/tasks` | `internal/ai/tasks.go:894` |
| GET | `/api/v1/ai/tasks/{id}` | `internal/ai/tasks.go:896` |
| GET | `/api/v1/ai/tasks/{id}/approvals` | `internal/ai/tasks.go:905` |
| POST | `/api/v1/ai/tasks/{id}/approvals/{call_id}` | `internal/ai/tasks.go:906` |
| GET | `/api/v1/ai/tasks/{id}/artifacts` | `internal/ai/tasks.go:901` |
| POST | `/api/v1/ai/tasks/{id}/cancel` | `internal/ai/tasks.go:898` |
| GET | `/api/v1/ai/tasks/{id}/events` | `internal/ai/tasks.go:899` |
| POST | `/api/v1/ai/tasks/{id}/pause` | `internal/ai/tasks.go:909` |
| POST | `/api/v1/ai/tasks/{id}/plan` | `internal/ai/tasks.go:903` |
| PUT | `/api/v1/ai/tasks/{id}/plan` | `internal/ai/tasks.go:904` |
| POST | `/api/v1/ai/tasks/{id}/priority` | `internal/ai/tasks.go:913` |
| POST | `/api/v1/ai/tasks/{id}/resume` | `internal/ai/tasks.go:910` |
| POST | `/api/v1/ai/tasks/{id}/start` | `internal/ai/tasks.go:897` |
| POST | `/api/v1/ai/tasks/{id}/steer` | `internal/ai/tasks.go:908` |

## `/api/v1/ai/tasks/queue` (1)

| Method | Path | 位置 |
| --- | --- | --- |
| GET | `/api/v1/ai/tasks/queue` | `internal/ai/tasks.go:912` |

## `/api/v1/ai/teams` (6)

| Method | Path | 位置 |
| --- | --- | --- |
| GET | `/api/v1/ai/teams` | `internal/ai/service.go:294` |
| POST | `/api/v1/ai/teams` | `internal/ai/service.go:293` |
| DELETE | `/api/v1/ai/teams/{id}` | `internal/ai/service.go:297` |
| GET | `/api/v1/ai/teams/{id}` | `internal/ai/service.go:295` |
| PUT | `/api/v1/ai/teams/{id}` | `internal/ai/service.go:296` |
| POST | `/api/v1/ai/teams/{id}/chat` | `internal/ai/service.go:298` |

## `/api/v1/ai/token-stats` (1)

| Method | Path | 位置 |
| --- | --- | --- |
| GET | `/api/v1/ai/token-stats` | `internal/ai/service.go:245` |

## `/api/v1/ai/tools` (2)

| Method | Path | 位置 |
| --- | --- | --- |
| GET | `/api/v1/ai/tools` | `internal/ai/service.go:234` |
| POST | `/api/v1/ai/tools/{name}/execute` | `internal/ai/service.go:236` |

## `/api/v1/ai/tools/custom` (4)

| Method | Path | 位置 |
| --- | --- | --- |
| GET | `/api/v1/ai/tools/custom` | `internal/ai/service.go:239` |
| POST | `/api/v1/ai/tools/custom` | `internal/ai/service.go:238` |
| DELETE | `/api/v1/ai/tools/custom/{id}` | `internal/ai/service.go:241` |
| PUT | `/api/v1/ai/tools/custom/{id}` | `internal/ai/service.go:240` |

## `/api/v1/ai/workforce-metrics` (1)

| Method | Path | 位置 |
| --- | --- | --- |
| GET | `/api/v1/ai/workforce-metrics` | `internal/ai/service.go:326` |

## `/api/v1/ai/workspace/generate` (1)

| Method | Path | 位置 |
| --- | --- | --- |
| POST | `/api/v1/ai/workspace/generate` | `internal/ai/service.go:397` |

## `/api/v1/ai/workspace/suggestions` (1)

| Method | Path | 位置 |
| --- | --- | --- |
| GET | `/api/v1/ai/workspace/suggestions` | `internal/ai/service.go:398` |

## `/api/v1/api-keys` (4)

| Method | Path | 位置 |
| --- | --- | --- |
| GET | `/api/v1/api-keys` | `internal/apikey/service.go:127` |
| POST | `/api/v1/api-keys` | `internal/apikey/service.go:128` |
| DELETE | `/api/v1/api-keys/{id}` | `internal/apikey/service.go:130` |
| POST | `/api/v1/api-keys/{id}/rotate` | `internal/apikey/service.go:129` |

## `/api/v1/audit/events` (2)

| Method | Path | 位置 |
| --- | --- | --- |
| GET | `/api/v1/audit/events` | `internal/audit/service.go:53` |
| POST | `/api/v1/audit/events` | `internal/audit/service.go:52` |

## `/api/v1/audit/validate-query` (1)

| Method | Path | 位置 |
| --- | --- | --- |
| POST | `/api/v1/audit/validate-query` | `internal/audit/service.go:54` |

## `/api/v1/auth/login` (1)

| Method | Path | 位置 |
| --- | --- | --- |
| POST | `/api/v1/auth/login` | `internal/user/service.go:46` |

## `/api/v1/auth/logout` (1)

| Method | Path | 位置 |
| --- | --- | --- |
| POST | `/api/v1/auth/logout` | `internal/user/service.go:47` |

## `/api/v1/auth/refresh` (1)

| Method | Path | 位置 |
| --- | --- | --- |
| POST | `/api/v1/auth/refresh` | `internal/user/service.go:48` |

## `/api/v1/automation/inbound/channels` (4)

| Method | Path | 位置 |
| --- | --- | --- |
| GET | `/api/v1/automation/inbound/channels` | `internal/automation/inboundconfig/store.go:153` |
| POST | `/api/v1/automation/inbound/channels` | `internal/automation/inboundconfig/store.go:154` |
| DELETE | `/api/v1/automation/inbound/channels/{id}` | `internal/automation/inboundconfig/store.go:156` |
| PUT | `/api/v1/automation/inbound/channels/{id}` | `internal/automation/inboundconfig/store.go:155` |

## `/api/v1/chatops/bindings` (2)

| Method | Path | 位置 |
| --- | --- | --- |
| GET | `/api/v1/chatops/bindings` | `internal/chatops/service.go:75` |
| DELETE | `/api/v1/chatops/bindings/{platform}/{chatID}` | `internal/chatops/service.go:76` |

## `/api/v1/chatops/callback` (1)

| Method | Path | 位置 |
| --- | --- | --- |
| POST | `/api/v1/chatops/callback/{channelID}` | `internal/chatops/service.go:65` |

## `/api/v1/chatops/channels` (5)

| Method | Path | 位置 |
| --- | --- | --- |
| GET | `/api/v1/chatops/channels` | `internal/chatops/service.go:68` |
| POST | `/api/v1/chatops/channels` | `internal/chatops/service.go:69` |
| DELETE | `/api/v1/chatops/channels/{id}` | `internal/chatops/service.go:71` |
| PUT | `/api/v1/chatops/channels/{id}` | `internal/chatops/service.go:70` |
| POST | `/api/v1/chatops/channels/{id}/test` | `internal/chatops/service.go:72` |

## `/api/v1/chatops/platforms` (1)

| Method | Path | 位置 |
| --- | --- | --- |
| GET | `/api/v1/chatops/platforms` | `internal/chatops/service.go:79` |

## `/api/v1/cloud/credentials` (3)

| Method | Path | 位置 |
| --- | --- | --- |
| GET | `/api/v1/cloud/credentials` | `internal/cloud/service.go:87` |
| POST | `/api/v1/cloud/credentials` | `internal/cloud/service.go:88` |
| DELETE | `/api/v1/cloud/credentials/{id}` | `internal/cloud/service.go:89` |

## `/api/v1/cloud/providers` (1)

| Method | Path | 位置 |
| --- | --- | --- |
| GET | `/api/v1/cloud/providers` | `internal/cloud/service.go:86` |

## `/api/v1/cloud/resources/list` (1)

| Method | Path | 位置 |
| --- | --- | --- |
| POST | `/api/v1/cloud/resources/list` | `internal/cloud/service.go:90` |

## `/api/v1/cloud/resources/sync` (1)

| Method | Path | 位置 |
| --- | --- | --- |
| POST | `/api/v1/cloud/resources/sync` | `internal/cloud/service.go:91` |

## `/api/v1/cmdb/app-topology` (1)

| Method | Path | 位置 |
| --- | --- | --- |
| GET | `/api/v1/cmdb/app-topology` | `internal/cmdb/service.go:215` |

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
| GET | `/api/v1/cmdb/ci` | `internal/cmdb/service.go:113` |
| POST | `/api/v1/cmdb/ci` | `internal/cmdb/service.go:116` |
| DELETE | `/api/v1/cmdb/ci/{id}` | `internal/cmdb/service.go:124` |
| GET | `/api/v1/cmdb/ci/{id}` | `internal/cmdb/service.go:122` |
| PUT | `/api/v1/cmdb/ci/{id}` | `internal/cmdb/service.go:123` |
| POST | `/api/v1/cmdb/ci/{id}/archive` | `internal/cmdb/service.go:125` |
| GET | `/api/v1/cmdb/ci/{id}/audit` | `internal/cmdb/service.go:127` |
| POST | `/api/v1/cmdb/ci/{id}/restore` | `internal/cmdb/service.go:126` |

## `/api/v1/cmdb/ci/batch` (1)

| Method | Path | 位置 |
| --- | --- | --- |
| POST | `/api/v1/cmdb/ci/batch` | `internal/cmdb/service.go:117` |

## `/api/v1/cmdb/ci/batch-delete` (1)

| Method | Path | 位置 |
| --- | --- | --- |
| POST | `/api/v1/cmdb/ci/batch-delete` | `internal/cmdb/service.go:119` |

## `/api/v1/cmdb/ci/batch-update` (1)

| Method | Path | 位置 |
| --- | --- | --- |
| POST | `/api/v1/cmdb/ci/batch-update` | `internal/cmdb/service.go:118` |

## `/api/v1/cmdb/ci/export` (1)

| Method | Path | 位置 |
| --- | --- | --- |
| GET | `/api/v1/cmdb/ci/export` | `internal/cmdb/service.go:114` |

## `/api/v1/cmdb/ci/import` (1)

| Method | Path | 位置 |
| --- | --- | --- |
| POST | `/api/v1/cmdb/ci/import` | `internal/cmdb/service.go:115` |

## `/api/v1/cmdb/collect/report` (1)

| Method | Path | 位置 |
| --- | --- | --- |
| POST | `/api/v1/cmdb/collect/report` | `internal/cmdb/service.go:121` |

## `/api/v1/cmdb/collection-policy` (2)

| Method | Path | 位置 |
| --- | --- | --- |
| GET | `/api/v1/cmdb/collection-policy` | `internal/cmdb/service.go:247` |
| PUT | `/api/v1/cmdb/collection-policy` | `internal/cmdb/service.go:248` |

## `/api/v1/cmdb/collection-tasks` (5)

| Method | Path | 位置 |
| --- | --- | --- |
| GET | `/api/v1/cmdb/collection-tasks` | `internal/cmdb/service.go:249` |
| POST | `/api/v1/cmdb/collection-tasks` | `internal/cmdb/service.go:250` |
| DELETE | `/api/v1/cmdb/collection-tasks/{id}` | `internal/cmdb/service.go:252` |
| PUT | `/api/v1/cmdb/collection-tasks/{id}` | `internal/cmdb/service.go:251` |
| POST | `/api/v1/cmdb/collection-tasks/{id}/run` | `internal/cmdb/service.go:253` |

## `/api/v1/cmdb/collectors` (1)

| Method | Path | 位置 |
| --- | --- | --- |
| GET | `/api/v1/cmdb/collectors` | `internal/cmdb/service.go:179` |

## `/api/v1/cmdb/dashboard` (2)

| Method | Path | 位置 |
| --- | --- | --- |
| GET | `/api/v1/cmdb/dashboard` | `internal/cmdb/service.go:200` |
| PUT | `/api/v1/cmdb/dashboard` | `internal/cmdb/service.go:201` |

## `/api/v1/cmdb/dashboard/insights` (1)

| Method | Path | 位置 |
| --- | --- | --- |
| GET | `/api/v1/cmdb/dashboard/insights` | `internal/cmdb/service.go:202` |

## `/api/v1/cmdb/data-policies` (3)

| Method | Path | 位置 |
| --- | --- | --- |
| GET | `/api/v1/cmdb/data-policies` | `internal/cmdb/service.go:136` |
| POST | `/api/v1/cmdb/data-policies` | `internal/cmdb/service.go:137` |
| DELETE | `/api/v1/cmdb/data-policies/{id}` | `internal/cmdb/service.go:138` |

## `/api/v1/cmdb/discovery` (8)

| Method | Path | 位置 |
| --- | --- | --- |
| GET | `/api/v1/cmdb/discovery` | `internal/cmdb/service.go:236` |
| POST | `/api/v1/cmdb/discovery` | `internal/cmdb/service.go:237` |
| DELETE | `/api/v1/cmdb/discovery/{id}` | `internal/cmdb/service.go:239` |
| PUT | `/api/v1/cmdb/discovery/{id}` | `internal/cmdb/service.go:238` |
| GET | `/api/v1/cmdb/discovery/{id}/results` | `internal/cmdb/service.go:242` |
| POST | `/api/v1/cmdb/discovery/{id}/run` | `internal/cmdb/service.go:241` |
| POST | `/api/v1/cmdb/discovery/{resultId}/sync` | `internal/cmdb/service.go:243` |
| POST | `/api/v1/cmdb/discovery/{taskId}/sync-all` | `internal/cmdb/service.go:244` |

## `/api/v1/cmdb/discovery/types` (1)

| Method | Path | 位置 |
| --- | --- | --- |
| GET | `/api/v1/cmdb/discovery/types` | `internal/cmdb/service.go:240` |

## `/api/v1/cmdb/drift` (2)

| Method | Path | 位置 |
| --- | --- | --- |
| GET | `/api/v1/cmdb/drift` | `internal/cmdb/service.go:140` |
| POST | `/api/v1/cmdb/drift/{id}/resolve` | `internal/cmdb/service.go:142` |

## `/api/v1/cmdb/field-rules` (3)

| Method | Path | 位置 |
| --- | --- | --- |
| GET | `/api/v1/cmdb/field-rules` | `internal/cmdb/service.go:218` |
| POST | `/api/v1/cmdb/field-rules` | `internal/cmdb/service.go:219` |
| DELETE | `/api/v1/cmdb/field-rules/{id}` | `internal/cmdb/service.go:220` |

## `/api/v1/cmdb/history` (2)

| Method | Path | 位置 |
| --- | --- | --- |
| GET | `/api/v1/cmdb/history` | `internal/cmdb/service.go:231` |
| GET | `/api/v1/cmdb/history/{ciId}` | `internal/cmdb/service.go:232` |

## `/api/v1/cmdb/history/detail` (1)

| Method | Path | 位置 |
| --- | --- | --- |
| GET | `/api/v1/cmdb/history/detail/{id}` | `internal/cmdb/service.go:233` |

## `/api/v1/cmdb/idc` (5)

| Method | Path | 位置 |
| --- | --- | --- |
| GET | `/api/v1/cmdb/idc` | `internal/cmdb/service.go:205` |
| POST | `/api/v1/cmdb/idc` | `internal/cmdb/service.go:206` |
| GET | `/api/v1/cmdb/idc/{idcId}/racks` | `internal/cmdb/service.go:208` |
| POST | `/api/v1/cmdb/idc/{idcId}/racks` | `internal/cmdb/service.go:209` |
| DELETE | `/api/v1/cmdb/idc/{id}` | `internal/cmdb/service.go:207` |

## `/api/v1/cmdb/idc/panorama` (1)

| Method | Path | 位置 |
| --- | --- | --- |
| GET | `/api/v1/cmdb/idc/panorama` | `internal/cmdb/service.go:141` |

## `/api/v1/cmdb/impact` (1)

| Method | Path | 位置 |
| --- | --- | --- |
| GET | `/api/v1/cmdb/impact/{id}` | `internal/cmdb/service.go:167` |

## `/api/v1/cmdb/model-categories` (3)

| Method | Path | 位置 |
| --- | --- | --- |
| GET | `/api/v1/cmdb/model-categories` | `internal/cmdb/service.go:194` |
| POST | `/api/v1/cmdb/model-categories` | `internal/cmdb/service.go:195` |
| DELETE | `/api/v1/cmdb/model-categories/{id}` | `internal/cmdb/service.go:196` |

## `/api/v1/cmdb/model-type` (9)

| Method | Path | 位置 |
| --- | --- | --- |
| GET | `/api/v1/cmdb/model-type` | `internal/cmdb/service.go:182` |
| POST | `/api/v1/cmdb/model-type` | `internal/cmdb/service.go:181` |
| DELETE | `/api/v1/cmdb/model-type/{id}` | `internal/cmdb/service.go:187` |
| GET | `/api/v1/cmdb/model-type/{id}` | `internal/cmdb/service.go:183` |
| PUT | `/api/v1/cmdb/model-type/{id}` | `internal/cmdb/service.go:186` |
| GET | `/api/v1/cmdb/model-type/{id}/ci-field-usage` | `internal/cmdb/service.go:185` |
| GET | `/api/v1/cmdb/model-type/{id}/field-usage` | `internal/cmdb/service.go:184` |
| POST | `/api/v1/cmdb/model-type/{id}/rollback/{version}` | `internal/cmdb/service.go:189` |
| GET | `/api/v1/cmdb/model-type/{id}/versions` | `internal/cmdb/service.go:188` |

## `/api/v1/cmdb/model-type/export` (1)

| Method | Path | 位置 |
| --- | --- | --- |
| GET | `/api/v1/cmdb/model-type/export` | `internal/cmdb/service.go:190` |

## `/api/v1/cmdb/model-type/import` (1)

| Method | Path | 位置 |
| --- | --- | --- |
| POST | `/api/v1/cmdb/model-type/import` | `internal/cmdb/service.go:191` |

## `/api/v1/cmdb/onboard` (1)

| Method | Path | 位置 |
| --- | --- | --- |
| POST | `/api/v1/cmdb/onboard` | `internal/cmdb/service.go:254` |

## `/api/v1/cmdb/path` (1)

| Method | Path | 位置 |
| --- | --- | --- |
| GET | `/api/v1/cmdb/path` | `internal/cmdb/service.go:169` |

## `/api/v1/cmdb/quality` (1)

| Method | Path | 位置 |
| --- | --- | --- |
| GET | `/api/v1/cmdb/quality` | `internal/cmdb/service.go:257` |

## `/api/v1/cmdb/quality/fix` (1)

| Method | Path | 位置 |
| --- | --- | --- |
| POST | `/api/v1/cmdb/quality/fix` | `internal/cmdb/service.go:258` |

## `/api/v1/cmdb/query/chain` (1)

| Method | Path | 位置 |
| --- | --- | --- |
| POST | `/api/v1/cmdb/query/chain` | `internal/cmdb/service.go:130` |

## `/api/v1/cmdb/racks` (3)

| Method | Path | 位置 |
| --- | --- | --- |
| DELETE | `/api/v1/cmdb/racks/{id}` | `internal/cmdb/service.go:210` |
| GET | `/api/v1/cmdb/racks/{id}/units` | `internal/cmdb/service.go:211` |
| POST | `/api/v1/cmdb/racks/{id}/units` | `internal/cmdb/service.go:212` |

## `/api/v1/cmdb/reconcile/candidates` (1)

| Method | Path | 位置 |
| --- | --- | --- |
| GET | `/api/v1/cmdb/reconcile/candidates` | `internal/cmdb/service.go:133` |

## `/api/v1/cmdb/reconcile/merge` (1)

| Method | Path | 位置 |
| --- | --- | --- |
| POST | `/api/v1/cmdb/reconcile/merge` | `internal/cmdb/service.go:134` |

## `/api/v1/cmdb/registry/promote` (1)

| Method | Path | 位置 |
| --- | --- | --- |
| POST | `/api/v1/cmdb/registry/promote` | `internal/cmdb/service.go:263` |

## `/api/v1/cmdb/registry/propose` (1)

| Method | Path | 位置 |
| --- | --- | --- |
| POST | `/api/v1/cmdb/registry/propose` | `internal/cmdb/service.go:262` |

## `/api/v1/cmdb/registry/resolve` (1)

| Method | Path | 位置 |
| --- | --- | --- |
| POST | `/api/v1/cmdb/registry/resolve` | `internal/cmdb/service.go:261` |

## `/api/v1/cmdb/relation` (3)

| Method | Path | 位置 |
| --- | --- | --- |
| GET | `/api/v1/cmdb/relation` | `internal/cmdb/service.go:174` |
| POST | `/api/v1/cmdb/relation` | `internal/cmdb/service.go:175` |
| DELETE | `/api/v1/cmdb/relation/{id}` | `internal/cmdb/service.go:176` |

## `/api/v1/cmdb/relation/batch` (1)

| Method | Path | 位置 |
| --- | --- | --- |
| POST | `/api/v1/cmdb/relation/batch` | `internal/cmdb/service.go:131` |

## `/api/v1/cmdb/saved-queries` (4)

| Method | Path | 位置 |
| --- | --- | --- |
| GET | `/api/v1/cmdb/saved-queries` | `internal/cmdb/service.go:159` |
| POST | `/api/v1/cmdb/saved-queries` | `internal/cmdb/service.go:160` |
| DELETE | `/api/v1/cmdb/saved-queries/{id}` | `internal/cmdb/service.go:162` |
| GET | `/api/v1/cmdb/saved-queries/{id}/resolve` | `internal/cmdb/service.go:161` |

## `/api/v1/cmdb/search` (1)

| Method | Path | 位置 |
| --- | --- | --- |
| GET | `/api/v1/cmdb/search` | `internal/cmdb/service.go:128` |

## `/api/v1/cmdb/snapshots` (4)

| Method | Path | 位置 |
| --- | --- | --- |
| GET | `/api/v1/cmdb/snapshots` | `internal/cmdb/service.go:146` |
| POST | `/api/v1/cmdb/snapshots` | `internal/cmdb/service.go:145` |
| DELETE | `/api/v1/cmdb/snapshots/{id}` | `internal/cmdb/service.go:149` |
| GET | `/api/v1/cmdb/snapshots/{id}` | `internal/cmdb/service.go:148` |

## `/api/v1/cmdb/snapshots/compare` (1)

| Method | Path | 位置 |
| --- | --- | --- |
| POST | `/api/v1/cmdb/snapshots/compare` | `internal/cmdb/service.go:147` |

## `/api/v1/cmdb/stats` (1)

| Method | Path | 位置 |
| --- | --- | --- |
| GET | `/api/v1/cmdb/stats` | `internal/cmdb/service.go:199` |

## `/api/v1/cmdb/subgraph` (1)

| Method | Path | 位置 |
| --- | --- | --- |
| GET | `/api/v1/cmdb/subgraph` | `internal/cmdb/service.go:166` |

## `/api/v1/cmdb/subscription-deliveries` (1)

| Method | Path | 位置 |
| --- | --- | --- |
| GET | `/api/v1/cmdb/subscription-deliveries` | `internal/cmdb/service.go:228` |

## `/api/v1/cmdb/subscriptions` (5)

| Method | Path | 位置 |
| --- | --- | --- |
| GET | `/api/v1/cmdb/subscriptions` | `internal/cmdb/service.go:223` |
| POST | `/api/v1/cmdb/subscriptions` | `internal/cmdb/service.go:224` |
| DELETE | `/api/v1/cmdb/subscriptions/{id}` | `internal/cmdb/service.go:226` |
| PUT | `/api/v1/cmdb/subscriptions/{id}` | `internal/cmdb/service.go:225` |
| POST | `/api/v1/cmdb/subscriptions/{id}/test` | `internal/cmdb/service.go:227` |

## `/api/v1/cmdb/topology` (1)

| Method | Path | 位置 |
| --- | --- | --- |
| GET | `/api/v1/cmdb/topology/{id}` | `internal/cmdb/service.go:164` |

## `/api/v1/cmdb/usage/errors` (1)

| Method | Path | 位置 |
| --- | --- | --- |
| GET | `/api/v1/cmdb/usage/errors` | `internal/cmdb/service.go:155` |

## `/api/v1/cmdb/usage/hotspots` (1)

| Method | Path | 位置 |
| --- | --- | --- |
| GET | `/api/v1/cmdb/usage/hotspots` | `internal/cmdb/service.go:154` |

## `/api/v1/cmdb/usage/status` (1)

| Method | Path | 位置 |
| --- | --- | --- |
| GET | `/api/v1/cmdb/usage/status` | `internal/cmdb/service.go:156` |

## `/api/v1/cmdb/usage/summary` (1)

| Method | Path | 位置 |
| --- | --- | --- |
| GET | `/api/v1/cmdb/usage/summary` | `internal/cmdb/service.go:153` |

## `/api/v1/collect-metrics` (1)

| Method | Path | 位置 |
| --- | --- | --- |
| GET | `/api/v1/collect-metrics` | `internal/agentmgr/service.go:817` |

## `/api/v1/collect-tasks` (5)

| Method | Path | 位置 |
| --- | --- | --- |
| GET | `/api/v1/collect-tasks` | `internal/agentmgr/service.go:815` |
| POST | `/api/v1/collect-tasks` | `internal/agentmgr/service.go:814` |
| DELETE | `/api/v1/collect-tasks/{id}` | `internal/agentmgr/service.go:819` |
| PUT | `/api/v1/collect-tasks/{id}` | `internal/agentmgr/service.go:818` |
| GET | `/api/v1/collect-tasks/{id}/runs` | `internal/agentmgr/service.go:816` |

## `/api/v1/deploy/apps` (4)

| Method | Path | 位置 |
| --- | --- | --- |
| GET | `/api/v1/deploy/apps` | `internal/deploy/service.go:35` |
| POST | `/api/v1/deploy/apps` | `internal/deploy/service.go:36` |
| DELETE | `/api/v1/deploy/apps/{id}` | `internal/deploy/service.go:38` |
| PUT | `/api/v1/deploy/apps/{id}` | `internal/deploy/service.go:37` |

## `/api/v1/deploy/environments` (4)

| Method | Path | 位置 |
| --- | --- | --- |
| GET | `/api/v1/deploy/environments` | `internal/deploy/service.go:49` |
| POST | `/api/v1/deploy/environments` | `internal/deploy/service.go:50` |
| DELETE | `/api/v1/deploy/environments/{id}` | `internal/deploy/service.go:52` |
| PUT | `/api/v1/deploy/environments/{id}` | `internal/deploy/service.go:51` |

## `/api/v1/deploy/history` (1)

| Method | Path | 位置 |
| --- | --- | --- |
| GET | `/api/v1/deploy/history` | `internal/deploy/service.go:54` |

## `/api/v1/deploy/releases` (8)

| Method | Path | 位置 |
| --- | --- | --- |
| GET | `/api/v1/deploy/releases` | `internal/deploy/service.go:40` |
| POST | `/api/v1/deploy/releases` | `internal/deploy/service.go:41` |
| DELETE | `/api/v1/deploy/releases/{id}` | `internal/deploy/service.go:43` |
| PUT | `/api/v1/deploy/releases/{id}` | `internal/deploy/service.go:42` |
| POST | `/api/v1/deploy/releases/{id}/approve` | `internal/deploy/service.go:44` |
| POST | `/api/v1/deploy/releases/{id}/deploy` | `internal/deploy/service.go:46` |
| POST | `/api/v1/deploy/releases/{id}/reject` | `internal/deploy/service.go:45` |
| POST | `/api/v1/deploy/releases/{id}/rollback` | `internal/deploy/service.go:47` |

## `/api/v1/files` (5)

| Method | Path | 位置 |
| --- | --- | --- |
| GET | `/api/v1/files` | `internal/agentmgr/service.go:825` |
| DELETE | `/api/v1/files/{file_id}` | `internal/agentmgr/service.go:826` |
| GET | `/api/v1/files/{file_id}` | `internal/agentmgr/service.go:797` |
| GET | `/api/v1/files/{file_id}` | `cmd/proxy/fileproxy.go:38` |
| GET | `/api/v1/files/{file_id}/meta` | `internal/agentmgr/service.go:827` |

## `/api/v1/files/upload` (1)

| Method | Path | 位置 |
| --- | --- | --- |
| POST | `/api/v1/files/upload` | `internal/agentmgr/service.go:824` |

## `/api/v1/iam/groups` (5)

| Method | Path | 位置 |
| --- | --- | --- |
| GET | `/api/v1/iam/groups` | `internal/iam/service.go:490` |
| POST | `/api/v1/iam/groups` | `internal/iam/service.go:491` |
| DELETE | `/api/v1/iam/groups/{id}` | `internal/iam/service.go:494` |
| GET | `/api/v1/iam/groups/{id}` | `internal/iam/service.go:492` |
| PUT | `/api/v1/iam/groups/{id}` | `internal/iam/service.go:493` |

## `/api/v1/iam/me/permissions` (1)

| Method | Path | 位置 |
| --- | --- | --- |
| GET | `/api/v1/iam/me/permissions` | `internal/iam/service.go:499` |

## `/api/v1/iam/permissions` (1)

| Method | Path | 位置 |
| --- | --- | --- |
| GET | `/api/v1/iam/permissions` | `internal/iam/service.go:482` |

## `/api/v1/iam/roles` (5)

| Method | Path | 位置 |
| --- | --- | --- |
| GET | `/api/v1/iam/roles` | `internal/iam/service.go:484` |
| POST | `/api/v1/iam/roles` | `internal/iam/service.go:485` |
| DELETE | `/api/v1/iam/roles/{id}` | `internal/iam/service.go:488` |
| GET | `/api/v1/iam/roles/{id}` | `internal/iam/service.go:486` |
| PUT | `/api/v1/iam/roles/{id}` | `internal/iam/service.go:487` |

## `/api/v1/iam/users` (2)

| Method | Path | 位置 |
| --- | --- | --- |
| GET | `/api/v1/iam/users/{user_id}/roles` | `internal/iam/service.go:496` |
| PUT | `/api/v1/iam/users/{user_id}/roles` | `internal/iam/service.go:497` |

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
| POST | `/api/v1/itsm/catalog/{id}/fulfill` | `internal/itsm/service.go:237` |

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
| GET | `/api/v1/itsm/changes` | `internal/itsm/service.go:271` |
| POST | `/api/v1/itsm/changes` | `internal/itsm/service.go:270` |
| GET | `/api/v1/itsm/changes/{id}` | `internal/itsm/service.go:272` |
| POST | `/api/v1/itsm/changes/{id}/execute` | `internal/itsm/service.go:274` |
| POST | `/api/v1/itsm/changes/{id}/vote` | `internal/itsm/service.go:273` |

## `/api/v1/itsm/form-dicts` (3)

| Method | Path | 位置 |
| --- | --- | --- |
| GET | `/api/v1/itsm/form-dicts` | `internal/itsm/service.go:264` |
| POST | `/api/v1/itsm/form-dicts` | `internal/itsm/service.go:265` |
| DELETE | `/api/v1/itsm/form-dicts/{id}` | `internal/itsm/service.go:266` |

## `/api/v1/itsm/form-options` (1)

| Method | Path | 位置 |
| --- | --- | --- |
| GET | `/api/v1/itsm/form-options` | `internal/itsm/service.go:267` |

## `/api/v1/itsm/forms` (2)

| Method | Path | 位置 |
| --- | --- | --- |
| GET | `/api/v1/itsm/forms` | `internal/itsm/service.go:261` |
| PUT | `/api/v1/itsm/forms` | `internal/itsm/service.go:262` |

## `/api/v1/itsm/ola/policies` (4)

| Method | Path | 位置 |
| --- | --- | --- |
| GET | `/api/v1/itsm/ola/policies` | `internal/itsm/ola/ola.go:120` |
| POST | `/api/v1/itsm/ola/policies` | `internal/itsm/ola/ola.go:121` |
| DELETE | `/api/v1/itsm/ola/policies/{id}` | `internal/itsm/ola/ola.go:123` |
| PUT | `/api/v1/itsm/ola/policies/{id}` | `internal/itsm/ola/ola.go:122` |

## `/api/v1/itsm/processes` (8)

| Method | Path | 位置 |
| --- | --- | --- |
| GET | `/api/v1/itsm/processes` | `internal/itsm/process/service.go:149` |
| POST | `/api/v1/itsm/processes` | `internal/itsm/process/service.go:150` |
| DELETE | `/api/v1/itsm/processes/{id}` | `internal/itsm/process/service.go:153` |
| GET | `/api/v1/itsm/processes/{id}` | `internal/itsm/process/service.go:151` |
| PUT | `/api/v1/itsm/processes/{id}` | `internal/itsm/process/service.go:152` |
| GET | `/api/v1/itsm/processes/{id}/bpmn` | `internal/itsm/process/service.go:154` |
| GET | `/api/v1/itsm/processes/{id}/versions` | `internal/itsm/process/service.go:156` |
| GET | `/api/v1/itsm/processes/{id}/versions/{version}` | `internal/itsm/process/service.go:157` |

## `/api/v1/itsm/processes/import-bpmn` (1)

| Method | Path | 位置 |
| --- | --- | --- |
| POST | `/api/v1/itsm/processes/import-bpmn` | `internal/itsm/process/service.go:155` |

## `/api/v1/itsm/sla/breached` (1)

| Method | Path | 位置 |
| --- | --- | --- |
| GET | `/api/v1/itsm/sla/breached` | `internal/itsm/service.go:258` |

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
| GET | `/api/v1/itsm/sla/stats` | `internal/itsm/service.go:257` |

## `/api/v1/itsm/stats` (1)

| Method | Path | 位置 |
| --- | --- | --- |
| GET | `/api/v1/itsm/stats` | `internal/itsm/service.go:239` |

## `/api/v1/itsm/stats/export/csv` (1)

| Method | Path | 位置 |
| --- | --- | --- |
| GET | `/api/v1/itsm/stats/export/csv` | `internal/itsm/service.go:278` |

## `/api/v1/itsm/tickets` (36)

| Method | Path | 位置 |
| --- | --- | --- |
| GET | `/api/v1/itsm/tickets` | `internal/itsm/service.go:217` |
| POST | `/api/v1/itsm/tickets` | `internal/itsm/service.go:215` |
| GET | `/api/v1/itsm/tickets/{id}` | `internal/itsm/service.go:216` |
| PUT | `/api/v1/itsm/tickets/{id}` | `internal/itsm/service.go:235` |
| POST | `/api/v1/itsm/tickets/{id}/approve` | `internal/itsm/service.go:219` |
| POST | `/api/v1/itsm/tickets/{id}/assign` | `internal/itsm/service.go:224` |
| GET | `/api/v1/itsm/tickets/{id}/attachments` | `internal/itsm/service.go:253` |
| POST | `/api/v1/itsm/tickets/{id}/attachments` | `internal/itsm/service.go:252` |
| DELETE | `/api/v1/itsm/tickets/{id}/attachments/{aid}` | `internal/itsm/service.go:255` |
| GET | `/api/v1/itsm/tickets/{id}/attachments/{aid}` | `internal/itsm/service.go:254` |
| POST | `/api/v1/itsm/tickets/{id}/close` | `internal/itsm/service.go:225` |
| GET | `/api/v1/itsm/tickets/{id}/comments` | `internal/itsm/service.go:242` |
| POST | `/api/v1/itsm/tickets/{id}/comments` | `internal/itsm/service.go:241` |
| DELETE | `/api/v1/itsm/tickets/{id}/comments/{cid}` | `internal/itsm/service.go:244` |
| PUT | `/api/v1/itsm/tickets/{id}/comments/{cid}` | `internal/itsm/service.go:243` |
| POST | `/api/v1/itsm/tickets/{id}/complete` | `internal/itsm/service.go:222` |
| PATCH | `/api/v1/itsm/tickets/{id}/custom-fields` | `internal/itsm/service.go:268` |
| GET | `/api/v1/itsm/tickets/{id}/events` | `internal/itsm/service.go:238` |
| POST | `/api/v1/itsm/tickets/{id}/execute` | `internal/itsm/service.go:221` |
| POST | `/api/v1/itsm/tickets/{id}/hold` | `internal/itsm/service.go:231` |
| GET | `/api/v1/itsm/tickets/{id}/process` | `internal/itsm/service.go:218` |
| POST | `/api/v1/itsm/tickets/{id}/reject` | `internal/itsm/service.go:220` |
| POST | `/api/v1/itsm/tickets/{id}/relate` | `internal/itsm/service.go:234` |
| POST | `/api/v1/itsm/tickets/{id}/remind` | `internal/itsm/service.go:233` |
| POST | `/api/v1/itsm/tickets/{id}/reopen` | `internal/itsm/service.go:229` |
| POST | `/api/v1/itsm/tickets/{id}/resubmit` | `internal/itsm/service.go:230` |
| POST | `/api/v1/itsm/tickets/{id}/resume` | `internal/itsm/service.go:232` |
| POST | `/api/v1/itsm/tickets/{id}/rollback` | `internal/itsm/service.go:226` |
| GET | `/api/v1/itsm/tickets/{id}/sla` | `internal/itsm/service.go:259` |
| POST | `/api/v1/itsm/tickets/{id}/submit` | `internal/itsm/service.go:223` |
| GET | `/api/v1/itsm/tickets/{id}/subtasks` | `internal/itsm/service.go:247` |
| POST | `/api/v1/itsm/tickets/{id}/subtasks` | `internal/itsm/service.go:246` |
| DELETE | `/api/v1/itsm/tickets/{id}/subtasks/{sid}` | `internal/itsm/service.go:250` |
| GET | `/api/v1/itsm/tickets/{id}/subtasks/{sid}` | `internal/itsm/service.go:248` |
| PUT | `/api/v1/itsm/tickets/{id}/subtasks/{sid}` | `internal/itsm/service.go:249` |
| POST | `/api/v1/itsm/tickets/{id}/withdraw` | `internal/itsm/service.go:228` |

## `/api/v1/itsm/tickets/export/csv` (1)

| Method | Path | 位置 |
| --- | --- | --- |
| GET | `/api/v1/itsm/tickets/export/csv` | `internal/itsm/service.go:277` |

## `/api/v1/itsm/tickets/import` (1)

| Method | Path | 位置 |
| --- | --- | --- |
| POST | `/api/v1/itsm/tickets/import` | `internal/itsm/service.go:276` |

## `/api/v1/keys` (6)

| Method | Path | 位置 |
| --- | --- | --- |
| DELETE | `/api/v1/keys` | `internal/keystore/service.go:140` |
| GET | `/api/v1/keys` | `internal/keystore/service.go:136` |
| POST | `/api/v1/keys` | `internal/keystore/service.go:135` |
| DELETE | `/api/v1/keys/{id}` | `internal/keystore/service.go:139` |
| GET | `/api/v1/keys/{id}` | `internal/keystore/service.go:137` |
| PUT | `/api/v1/keys/{id}` | `internal/keystore/service.go:138` |

## `/api/v1/knowledge` (22)

| Method | Path | 位置 |
| --- | --- | --- |
| GET | `/api/v1/knowledge` | `internal/knowledge/service.go:356` |
| POST | `/api/v1/knowledge` | `internal/knowledge/service.go:355` |
| DELETE | `/api/v1/knowledge/{id}` | `internal/knowledge/service.go:359` |
| GET | `/api/v1/knowledge/{id}` | `internal/knowledge/service.go:357` |
| PUT | `/api/v1/knowledge/{id}` | `internal/knowledge/service.go:358` |
| GET | `/api/v1/knowledge/{id}/attachments` | `internal/knowledge/service.go:375` |
| POST | `/api/v1/knowledge/{id}/attachments` | `internal/knowledge/service.go:374` |
| DELETE | `/api/v1/knowledge/{id}/attachments/{aid}` | `internal/knowledge/service.go:377` |
| GET | `/api/v1/knowledge/{id}/attachments/{aid}` | `internal/knowledge/service.go:376` |
| GET | `/api/v1/knowledge/{id}/attachments/{aid}/chunks` | `internal/knowledge/service.go:381` |
| POST | `/api/v1/knowledge/{id}/attachments/{aid}/reindex` | `internal/knowledge/service.go:379` |
| GET | `/api/v1/knowledge/{id}/attachments/{aid}/text` | `internal/knowledge/service.go:380` |
| POST | `/api/v1/knowledge/{id}/bookmark` | `internal/knowledge/social.go:152` |
| GET | `/api/v1/knowledge/{id}/chunks/{chunk}` | `internal/knowledge/service.go:386` |
| GET | `/api/v1/knowledge/{id}/comments` | `internal/knowledge/social.go:160` |
| POST | `/api/v1/knowledge/{id}/comments` | `internal/knowledge/social.go:159` |
| POST | `/api/v1/knowledge/{id}/follow` | `internal/knowledge/social.go:153` |
| POST | `/api/v1/knowledge/{id}/like` | `internal/knowledge/social.go:151` |
| POST | `/api/v1/knowledge/{id}/rate` | `internal/knowledge/social.go:158` |
| GET | `/api/v1/knowledge/{id}/stats` | `internal/knowledge/social.go:150` |
| GET | `/api/v1/knowledge/{id}/versions` | `internal/knowledge/service.go:366` |
| POST | `/api/v1/knowledge/{id}/versions/{version}/rollback` | `internal/knowledge/service.go:367` |

## `/api/v1/knowledge/ai-write` (1)

| Method | Path | 位置 |
| --- | --- | --- |
| POST | `/api/v1/knowledge/ai-write` | `internal/knowledge/service.go:372` |

## `/api/v1/knowledge/bookmarks` (1)

| Method | Path | 位置 |
| --- | --- | --- |
| GET | `/api/v1/knowledge/bookmarks` | `internal/knowledge/social.go:155` |

## `/api/v1/knowledge/categories` (4)

| Method | Path | 位置 |
| --- | --- | --- |
| GET | `/api/v1/knowledge/categories` | `internal/knowledge/service.go:362` |
| POST | `/api/v1/knowledge/categories` | `internal/knowledge/service.go:363` |
| DELETE | `/api/v1/knowledge/categories/{id}` | `internal/knowledge/service.go:365` |
| PUT | `/api/v1/knowledge/categories/{id}` | `internal/knowledge/service.go:364` |

## `/api/v1/knowledge/follow-updates` (1)

| Method | Path | 位置 |
| --- | --- | --- |
| GET | `/api/v1/knowledge/follow-updates` | `internal/knowledge/social.go:156` |

## `/api/v1/knowledge/follow-updates/read` (1)

| Method | Path | 位置 |
| --- | --- | --- |
| POST | `/api/v1/knowledge/follow-updates/read` | `internal/knowledge/social.go:157` |

## `/api/v1/knowledge/follows` (1)

| Method | Path | 位置 |
| --- | --- | --- |
| GET | `/api/v1/knowledge/follows` | `internal/knowledge/social.go:154` |

## `/api/v1/knowledge/import` (1)

| Method | Path | 位置 |
| --- | --- | --- |
| POST | `/api/v1/knowledge/import` | `internal/knowledge/service.go:368` |

## `/api/v1/knowledge/import-file` (1)

| Method | Path | 位置 |
| --- | --- | --- |
| POST | `/api/v1/knowledge/import-file` | `internal/knowledge/service.go:370` |

## `/api/v1/knowledge/import-url` (1)

| Method | Path | 位置 |
| --- | --- | --- |
| POST | `/api/v1/knowledge/import-url` | `internal/knowledge/service.go:383` |

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
| POST | `/api/v1/knowledge/reindex` | `internal/knowledge/service.go:360` |

## `/api/v1/knowledge/search-logs` (1)

| Method | Path | 位置 |
| --- | --- | --- |
| GET | `/api/v1/knowledge/search-logs` | `internal/knowledge/service.go:389` |

## `/api/v1/knowledge/search-stats` (1)

| Method | Path | 位置 |
| --- | --- | --- |
| GET | `/api/v1/knowledge/search-stats` | `internal/knowledge/service.go:390` |

## `/api/v1/lowcode/instances` (5)

| Method | Path | 位置 |
| --- | --- | --- |
| GET | `/api/v1/lowcode/instances` | `internal/lowcode/service/handler.go:33` |
| POST | `/api/v1/lowcode/instances` | `internal/lowcode/service/handler.go:32` |
| DELETE | `/api/v1/lowcode/instances/{id}` | `internal/lowcode/service/handler.go:36` |
| GET | `/api/v1/lowcode/instances/{id}` | `internal/lowcode/service/handler.go:34` |
| PUT | `/api/v1/lowcode/instances/{id}` | `internal/lowcode/service/handler.go:35` |

## `/api/v1/lowcode/models` (9)

| Method | Path | 位置 |
| --- | --- | --- |
| GET | `/api/v1/lowcode/models` | `internal/lowcode/service/handler.go:26` |
| POST | `/api/v1/lowcode/models` | `internal/lowcode/service/handler.go:25` |
| DELETE | `/api/v1/lowcode/models/{id}` | `internal/lowcode/service/handler.go:29` |
| GET | `/api/v1/lowcode/models/{id}` | `internal/lowcode/service/handler.go:27` |
| PUT | `/api/v1/lowcode/models/{id}` | `internal/lowcode/service/handler.go:28` |
| GET | `/api/v1/lowcode/models/{id}/versions` | `internal/lowcode/service/version_handler.go:29` |
| GET | `/api/v1/lowcode/models/{id}/versions/{versionId}` | `internal/lowcode/service/version_handler.go:30` |
| POST | `/api/v1/lowcode/models/{id}/versions/{versionId}/publish` | `internal/lowcode/service/version_handler.go:31` |
| POST | `/api/v1/lowcode/models/{id}/versions/{versionId}/rollback` | `internal/lowcode/service/version_handler.go:32` |

## `/api/v1/lowcode/pages` (9)

| Method | Path | 位置 |
| --- | --- | --- |
| GET | `/api/v1/lowcode/pages` | `internal/lowcode/service/handler.go:40` |
| POST | `/api/v1/lowcode/pages` | `internal/lowcode/service/handler.go:39` |
| DELETE | `/api/v1/lowcode/pages/{id}` | `internal/lowcode/service/handler.go:43` |
| GET | `/api/v1/lowcode/pages/{id}` | `internal/lowcode/service/handler.go:41` |
| PUT | `/api/v1/lowcode/pages/{id}` | `internal/lowcode/service/handler.go:42` |
| GET | `/api/v1/lowcode/pages/{id}/versions` | `internal/lowcode/service/version_handler.go:23` |
| GET | `/api/v1/lowcode/pages/{id}/versions/{versionId}` | `internal/lowcode/service/version_handler.go:24` |
| POST | `/api/v1/lowcode/pages/{id}/versions/{versionId}/publish` | `internal/lowcode/service/version_handler.go:25` |
| POST | `/api/v1/lowcode/pages/{id}/versions/{versionId}/rollback` | `internal/lowcode/service/version_handler.go:26` |

## `/api/v1/lowcode/sql` (1)

| Method | Path | 位置 |
| --- | --- | --- |
| GET | `/api/v1/lowcode/sql/{model_id}` | `internal/lowcode/service/handler.go:49` |

## `/api/v1/lowcode/validate` (1)

| Method | Path | 位置 |
| --- | --- | --- |
| POST | `/api/v1/lowcode/validate` | `internal/lowcode/service/handler.go:46` |

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
| GET | `/api/v1/monitor/alert-stats` | `internal/monitor/service.go:139` |

## `/api/v1/monitor/alerts` (3)

| Method | Path | 位置 |
| --- | --- | --- |
| GET | `/api/v1/monitor/alerts` | `internal/monitor/service.go:138` |
| POST | `/api/v1/monitor/alerts/{id}/ack` | `internal/monitor/service.go:143` |
| POST | `/api/v1/monitor/alerts/{id}/resolve` | `internal/monitor/service.go:144` |

## `/api/v1/monitor/alerts/rca` (1)

| Method | Path | 位置 |
| --- | --- | --- |
| GET | `/api/v1/monitor/alerts/rca` | `internal/monitor/service.go:140` |

## `/api/v1/monitor/alerts/rca/paths` (1)

| Method | Path | 位置 |
| --- | --- | --- |
| GET | `/api/v1/monitor/alerts/rca/paths` | `internal/monitor/service.go:141` |

## `/api/v1/monitor/metrics` (1)

| Method | Path | 位置 |
| --- | --- | --- |
| GET | `/api/v1/monitor/metrics` | `internal/monitor/service.go:148` |

## `/api/v1/monitor/plugins` (1)

| Method | Path | 位置 |
| --- | --- | --- |
| GET | `/api/v1/monitor/plugins` | `internal/monitor/service.go:149` |

## `/api/v1/monitor/predict` (1)

| Method | Path | 位置 |
| --- | --- | --- |
| POST | `/api/v1/monitor/predict` | `internal/monitor/service.go:142` |

## `/api/v1/monitor/probe` (1)

| Method | Path | 位置 |
| --- | --- | --- |
| POST | `/api/v1/monitor/probe` | `internal/monitor/service.go:137` |

## `/api/v1/monitor/rules` (3)

| Method | Path | 位置 |
| --- | --- | --- |
| GET | `/api/v1/monitor/rules` | `internal/monitor/service.go:145` |
| POST | `/api/v1/monitor/rules` | `internal/monitor/service.go:146` |
| DELETE | `/api/v1/monitor/rules/{id}` | `internal/monitor/service.go:147` |

## `/api/v1/monitor/targets` (4)

| Method | Path | 位置 |
| --- | --- | --- |
| GET | `/api/v1/monitor/targets` | `internal/monitor/service.go:133` |
| POST | `/api/v1/monitor/targets` | `internal/monitor/service.go:135` |
| DELETE | `/api/v1/monitor/targets/{id}` | `internal/monitor/service.go:136` |
| GET | `/api/v1/monitor/targets/{id}` | `internal/monitor/service.go:134` |

## `/api/v1/monitor/topology/parent` (1)

| Method | Path | 位置 |
| --- | --- | --- |
| POST | `/api/v1/monitor/topology/parent` | `internal/monitor/service.go:150` |

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
| GET | `/api/v1/oncall` | `internal/itsm/oncall/service.go:175` |
| POST | `/api/v1/oncall` | `internal/itsm/oncall/service.go:176` |

## `/api/v1/oncall/rotations` (4)

| Method | Path | 位置 |
| --- | --- | --- |
| GET | `/api/v1/oncall/rotations` | `internal/itsm/oncall/service.go:177` |
| POST | `/api/v1/oncall/rotations` | `internal/itsm/oncall/service.go:178` |
| DELETE | `/api/v1/oncall/rotations/{id}` | `internal/itsm/oncall/service.go:179` |
| GET | `/api/v1/oncall/rotations/{id}/oncall` | `internal/itsm/oncall/service.go:180` |

## `/api/v1/ops/health` (1)

| Method | Path | 位置 |
| --- | --- | --- |
| GET | `/api/v1/ops/health` | `internal/ops/service.go:59` |

## `/api/v1/ops/restart` (1)

| Method | Path | 位置 |
| --- | --- | --- |
| POST | `/api/v1/ops/restart` | `internal/ops/service.go:62` |

## `/api/v1/ops/status` (1)

| Method | Path | 位置 |
| --- | --- | --- |
| GET | `/api/v1/ops/status` | `internal/ops/service.go:60` |

## `/api/v1/ops/stop` (1)

| Method | Path | 位置 |
| --- | --- | --- |
| POST | `/api/v1/ops/stop` | `internal/ops/service.go:61` |

## `/api/v1/panorama` (1)

| Method | Path | 位置 |
| --- | --- | --- |
| GET | `/api/v1/panorama` | `internal/cmdb/service.go:172` |

## `/api/v1/proxies` (5)

| Method | Path | 位置 |
| --- | --- | --- |
| GET | `/api/v1/proxies` | `internal/agentmgr/service.go:841` |
| POST | `/api/v1/proxies` | `internal/agentmgr/service.go:842` |
| DELETE | `/api/v1/proxies/{id}` | `internal/agentmgr/service.go:845` |
| GET | `/api/v1/proxies/{id}` | `internal/agentmgr/service.go:843` |
| PUT | `/api/v1/proxies/{id}` | `internal/agentmgr/service.go:844` |

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
| POST | `/api/v1/sandbox/exec` | `internal/agentmgr/service.go:833` |

## `/api/v1/schedules` (5)

| Method | Path | 位置 |
| --- | --- | --- |
| GET | `/api/v1/schedules` | `internal/automation/scheduler/service.go:126` |
| POST | `/api/v1/schedules` | `internal/automation/scheduler/service.go:125` |
| DELETE | `/api/v1/schedules/{id}` | `internal/automation/scheduler/service.go:129` |
| GET | `/api/v1/schedules/{id}` | `internal/automation/scheduler/service.go:127` |
| PUT | `/api/v1/schedules/{id}` | `internal/automation/scheduler/service.go:128` |

## `/api/v1/scripts` (13)

| Method | Path | 位置 |
| --- | --- | --- |
| GET | `/api/v1/scripts` | `internal/automation/script/service.go:290` |
| POST | `/api/v1/scripts` | `internal/automation/script/service.go:289` |
| DELETE | `/api/v1/scripts/{id}` | `internal/automation/script/service.go:293` |
| GET | `/api/v1/scripts/{id}` | `internal/automation/script/service.go:291` |
| PUT | `/api/v1/scripts/{id}` | `internal/automation/script/service.go:292` |
| POST | `/api/v1/scripts/{id}/approve` | `internal/automation/script/service.go:302` |
| GET | `/api/v1/scripts/{id}/diff` | `internal/automation/script/service.go:298` |
| POST | `/api/v1/scripts/{id}/execute` | `internal/automation/script/service.go:294` |
| GET | `/api/v1/scripts/{id}/executions` | `internal/automation/script/service.go:295` |
| POST | `/api/v1/scripts/{id}/publish` | `internal/automation/script/service.go:301` |
| POST | `/api/v1/scripts/{id}/reject` | `internal/automation/script/service.go:303` |
| GET | `/api/v1/scripts/{id}/versions` | `internal/automation/script/service.go:296` |
| GET | `/api/v1/scripts/{id}/versions/{version}` | `internal/automation/script/service.go:297` |

## `/api/v1/scripts/ai-write` (1)

| Method | Path | 位置 |
| --- | --- | --- |
| POST | `/api/v1/scripts/ai-write` | `internal/automation/script/service.go:300` |

## `/api/v1/scripts/check` (1)

| Method | Path | 位置 |
| --- | --- | --- |
| POST | `/api/v1/scripts/check` | `internal/automation/script/service.go:299` |

## `/api/v1/system/config` (2)

| Method | Path | 位置 |
| --- | --- | --- |
| GET | `/api/v1/system/config` | `internal/sysconfig/service.go:144` |
| PUT | `/api/v1/system/config` | `internal/sysconfig/service.go:145` |

## `/api/v1/system/config/categories` (1)

| Method | Path | 位置 |
| --- | --- | --- |
| GET | `/api/v1/system/config/categories` | `internal/sysconfig/service.go:146` |

## `/api/v1/system/logs` (1)

| Method | Path | 位置 |
| --- | --- | --- |
| GET | `/api/v1/system/logs` | `internal/systemmon/service.go:78` |

## `/api/v1/system/status` (1)

| Method | Path | 位置 |
| --- | --- | --- |
| GET | `/api/v1/system/status` | `internal/systemmon/service.go:77` |

## `/api/v1/users` (6)

| Method | Path | 位置 |
| --- | --- | --- |
| GET | `/api/v1/users` | `internal/user/service.go:49` |
| POST | `/api/v1/users` | `internal/user/service.go:50` |
| DELETE | `/api/v1/users/{id}` | `internal/user/service.go:53` |
| GET | `/api/v1/users/{id}` | `internal/user/service.go:51` |
| PUT | `/api/v1/users/{id}` | `internal/user/service.go:52` |
| PUT | `/api/v1/users/{id}/password` | `internal/user/service.go:54` |

## `/api/v1/users/me` (1)

| Method | Path | 位置 |
| --- | --- | --- |
| GET | `/api/v1/users/me` | `internal/user/service.go:55` |

## `/api/v1/vuln/findings` (2)

| Method | Path | 位置 |
| --- | --- | --- |
| GET | `/api/v1/vuln/findings` | `internal/vuln/service.go:92` |
| PATCH | `/api/v1/vuln/findings/{id}` | `internal/vuln/service.go:93` |

## `/api/v1/vuln/scans` (5)

| Method | Path | 位置 |
| --- | --- | --- |
| GET | `/api/v1/vuln/scans` | `internal/vuln/service.go:86` |
| POST | `/api/v1/vuln/scans` | `internal/vuln/service.go:85` |
| DELETE | `/api/v1/vuln/scans/{id}` | `internal/vuln/service.go:89` |
| GET | `/api/v1/vuln/scans/{id}` | `internal/vuln/service.go:88` |
| GET | `/api/v1/vuln/scans/{id}/findings` | `internal/vuln/service.go:90` |

## `/api/v1/vuln/scans/export/csv` (1)

| Method | Path | 位置 |
| --- | --- | --- |
| GET | `/api/v1/vuln/scans/export/csv` | `internal/vuln/service.go:87` |

## `/api/v1/vuln/stats` (1)

| Method | Path | 位置 |
| --- | --- | --- |
| GET | `/api/v1/vuln/stats` | `internal/vuln/service.go:94` |

## `/api/v1/workflows` (7)

| Method | Path | 位置 |
| --- | --- | --- |
| GET | `/api/v1/workflows` | `internal/automation/workflow/service.go:241` |
| POST | `/api/v1/workflows` | `internal/automation/workflow/service.go:240` |
| DELETE | `/api/v1/workflows/{id}` | `internal/automation/workflow/service.go:244` |
| GET | `/api/v1/workflows/{id}` | `internal/automation/workflow/service.go:242` |
| PUT | `/api/v1/workflows/{id}` | `internal/automation/workflow/service.go:243` |
| POST | `/api/v1/workflows/{id}/execute` | `internal/automation/workflow/service.go:245` |
| GET | `/api/v1/workflows/{id}/executions` | `internal/automation/workflow/service.go:246` |

## `/api/v1/workflows/approvals` (3)

| Method | Path | 位置 |
| --- | --- | --- |
| GET | `/api/v1/workflows/approvals` | `internal/automation/workflow/service.go:248` |
| POST | `/api/v1/workflows/approvals/{id}/approve` | `internal/automation/workflow/service.go:249` |
| POST | `/api/v1/workflows/approvals/{id}/reject` | `internal/automation/workflow/service.go:250` |

## `/api/v1/workflows/executions` (1)

| Method | Path | 位置 |
| --- | --- | --- |
| POST | `/api/v1/workflows/executions/{exec_id}/cancel` | `internal/automation/workflow/service.go:251` |

## `/api/v1/workflows/templates/list` (1)

| Method | Path | 位置 |
| --- | --- | --- |
| GET | `/api/v1/workflows/templates/list` | `internal/automation/workflow/service.go:253` |

## `/swagger` (1)

| Method | Path | 位置 |
| --- | --- | --- |
| GET | `/swagger/doc.json` | `cmd/core/server.go:157` |

