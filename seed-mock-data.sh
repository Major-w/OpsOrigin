#!/bin/bash
# OpsOrigin mock data seeder — 给前端测试造数据
set -e
HOST="${1:-http://localhost:8080}"
echo "=== Seeding OpsOrigin @ $HOST ==="

post() { curl -s -X POST "$HOST$1" -H 'Content-Type: application/json' -d "$2" | python3 -c "import sys,json; d=json.load(sys.stdin); e=d.get('error',''); print(e and '❌ '+e[:60] or '✅ '+d.get('id','ok')[:20])" 2>/dev/null; }

echo "--- Scripts ---"
post '/api/v1/scripts' '{"name":"CPU监控检查","type":"shell","content":"#!/bin/bash\necho \"=== CPU Usage ===\"\ntop -bn1 | head -5\nOUT_cpu=$(top -bn1 | grep \"Cpu\" | awk \"{print \\$2}\")\necho \"OUT_cpu=$OUT_cpu\"\necho \"OK\"","description":"检查服务器CPU使用率","timeout":30,"run_as":""}'
post '/api/v1/scripts' '{"name":"磁盘空间检查","type":"shell","content":"#!/bin/bash\necho \"=== Disk Usage ===\"\ndf -h\nOUT_usage=$(df -h / | tail -1 | awk \"{print \\$5}\")\necho \"OUT_usage=$OUT_usage\"","description":"检查磁盘空间使用情况","timeout":30}'
post '/api/v1/scripts' '{"name":"服务状态检查","type":"python","content":"import os\nimport subprocess\nservices = [\"nginx\",\"mysql\",\"redis\"]\nfor s in services:\n    r = subprocess.run([\"systemctl\",\"is-active\",s],capture_output=True,text=True)\n    print(f\"{s}: {r.stdout.strip()}\")\n    print(f\"OUT_{s}={r.stdout.strip()}\")","description":"检查关键服务运行状态","timeout":60}'
post '/api/v1/scripts' '{"name":"Windows服务检查","type":"bat","content":"@echo off\necho === Windows Services ===\nsc query nginx\nsc query mysql\necho OUT_nginx=Running\necho OUT_mysql=Stopped","description":"Windows服务器服务状态检查","timeout":30}'

echo "--- AI Agents ---"
post '/api/v1/ai/agents' '{"name":"运维助手","model_name":"deepseek-chat","system_prompt":"你是资深Linux运维工程师，精通Shell/Python/Ansible。回答简洁、专业、可操作，优先给出命令而非长篇解释。"}'
post '/api/v1/ai/agents' '{"name":"DBA专家","model_name":"deepseek-chat","system_prompt":"你是资深DBA，精通MySQL/PostgreSQL/Redis。回答应包含具体的SQL或运维命令，关注性能优化和数据安全。"}'
post '/api/v1/ai/agents' '{"name":"安全审计","model_name":"deepseek-chat","system_prompt":"你是安全审计专家，擅长漏洞分析、权限审计、日志分析。回答应标注风险等级(高/中/低)和修复建议。"}'

echo "--- Notification Channels ---"
post '/api/v1/notification/channels' '{"name":"运维告警群","type":"wecom","config":{"webhook_url":"https://qyapi.weixin.qq.com/cgi-bin/webhook/send?key=mock"},"enabled":true}'
post '/api/v1/notification/channels' '{"name":"紧急通知邮件","type":"email","config":{"smtp_host":"smtp.company.com","smtp_port":"587","to":"ops@company.com"},"enabled":true}'

echo "--- Workflows ---"
post '/api/v1/workflows' '{"name":"日常巡检流水线","description":"每天早上执行CPU/磁盘/服务检查并汇总报告","steps":[{"tool":"llm_chat","input":{"prompt":"汇总巡检结果"}}]}'

echo ""
echo "=== Seed complete ==="