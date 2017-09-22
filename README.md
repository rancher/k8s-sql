SQL Storage Backend for Kubernetes
==================================

Use a DB such as MySQL, RDS, or Cloud SQL for your Kubernetes cluster state.

Why?
----

This has very little to do with the technical merits of etcd or a traditional SQL database.  Both are persistent systems that require consideration when running in production.  Given various factors in your organization a SQL database may be more desirable to run over etcd.  This may be due to in house expertise or the fact that services such as RDS and CloudSQL are readily available.

Another consideration is that SQL is a frontend added to many non-traditional databases.  Because this implementation uses very simple SQL statements (single table, no joins, simple SELECT, UPDATE, DELETE) it may be possible to leverage this interface to bring more storage backends to Kubernetes.

And lastly, I (@ibuildthecloud) like to experiement and often do things because I can :)

How?
----

Come to find out Kubernetes itself handles a lot of magic of data access.  The access patterns are quite basic.  While etcd has many incredibly useful features, a lot of them aren't used.  Kubernetes need basic CRUD operations, TTL, and change notifications.  Change notifications are the only thing that considers some thought.  Right now it's very simple and stupid and done based off the assumption that there is only one API server so changes are easy to track.  For HA something else will have to be implemented but there are many smart approaches for that I'm sure.


Try it out
----------

While the code is fairly generic SQL, it currently is only tested with MySQL.  Adopting to another RDBMS should be a trivial effort.  But now... use MySQL.


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

2. [Download](https://github.com/rancher/k8s-sql/releases/download/v0.0.1/kube-apiserver.xz)  this custom patched Kubernetes API Server 1.7.4 server [[Source]](https://github.com/rancher/kubernetes/releases/tag/v1.7.6-netes1)

3. Run with your usual args but add the additional arguments

```
--storage-backend=rdbms
--etcd-servers=mysql
--etcd-servers=k8s:k8s@tcp(localhost:3306)/k8s
--watch-cache=false
```

This assuming you have username/password as k8s/k8s and a database created called k8s.

Yeah, it's a bit hacky because the API server is sort of hard coded to etcd. Also I've only tested with watch-cache off.


Known Issues/Limitations
------------------------

1. List responses don't have a proper version on them (does it really matter??).
2. No HA apiserver support.  Watches are using in-memory stuff that won't allow multiple API servers.  If this basic implementation goes well shouldn't be terrible to add proper HA.
