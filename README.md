SQL Storage Backend for Kubernetes
==================================

Use a DB such as MySQL, RDS, or Cloud SQL for your Kubernetes cluster state.

Try it out
----------

While the code is fairly generic SQL, it currently is only tested with MySQL.  Adopting to other RDBMS should be a trivial effort.  But now... use MySQL.

1. Create a database with the following schema

```sql
CREATE TABLE `key_value` (
  `name` varchar(255) DEFAULT NULL,
  `value` mediumblob,
  `revision` bigint(20) DEFAULT NULL,
  `ttl` bigint(20) NOT NULL DEFAULT '0'
) ENGINE=InnoDB DEFAULT CHARSET=utf8;

ALTER TABLE `key_value`
  ADD UNIQUE KEY `uix_key_value_name` (`name`),
  ADD KEY `idx_key_value__ttl` (`ttl`);
```

2. Download this custom patched Kubernetes API Server 1.7.4 server

3. Run with the arguments
