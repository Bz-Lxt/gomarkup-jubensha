package repository

// int64Array 把 []int64 交给 pgx 作为 `= ANY($1)` 的实参。
//
// pgx v5 在 database/sql 模式下会把 []int64 映射为 Postgres 的 bigint[]，
// 因此不需要 pq.Array 之类的包装。集中在这里是为了：如果将来换驱动，
// 只改这一个函数，而不是散落在十几处查询里。
func int64Array(ids []int64) []int64 { return ids }
