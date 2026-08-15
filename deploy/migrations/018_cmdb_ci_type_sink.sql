-- OpsOrigin Core — 方案 C 存量 CI 下沉（大类 → 具体引擎子类）
-- 把 type_id 仍为 database/middleware 的旧 CI，按 attributes 里的
-- engine / middleware_type 下沉为具体叶子类型（mysql/oracle/postgresql/redis/...）。
-- 未知 engine 保留父类不迁移，避免制造孤儿类型。幂等：重复执行仅命中仍未下沉的行。

UPDATE cmdb_ci SET type_id = attributes->>'engine'
WHERE type_id = 'database' AND attributes ? 'engine'
  AND attributes->>'engine' IN ('mysql', 'oracle', 'postgresql');

UPDATE cmdb_ci SET type_id = attributes->>'middleware_type'
WHERE type_id = 'middleware' AND attributes ? 'middleware_type'
  AND attributes->>'middleware_type' IN ('redis', 'kafka', 'nginx', 'elasticsearch', 'zookeeper', 'rabbitmq');
