/// <reference types="node" />
// The tests run on Node: its SQLite stands in for D1, with the real schema.
import { readFileSync } from "node:fs";
import { join } from "node:path";
import { DatabaseSync } from "node:sqlite";

/** A statement as the fake D1 keeps it: its SQL and bound values. */
interface FakeStatement {
	sql: string;
	args: (string | number)[];
	bind(...args: (string | number)[]): FakeStatement;
}

/** A fresh in-memory database with the Worker's migrations applied. */
export function database(): DatabaseSync {
	const db = new DatabaseSync(":memory:");
	db.exec(readFileSync(join(import.meta.dirname, "../migrations/0001_daily.sql"), "utf8"));
	return db;
}

/** Just enough of D1 over a real SQLite database for the Workers' statements. */
export function d1(db: DatabaseSync): D1Database {
	const statement = (sql: string, args: (string | number)[] = []): FakeStatement => ({
		sql,
		args,
		bind: (...bound) => statement(sql, bound),
	});
	return {
		prepare: (sql: string) => statement(sql),
		// A batch is one transaction, as on D1; each statement answers its rows.
		async batch(statements: FakeStatement[]) {
			db.exec("BEGIN");
			try {
				const out = statements.map((s) => ({ success: true, results: db.prepare(s.sql).all(...s.args) }));
				db.exec("COMMIT");
				return out;
			} catch (err) {
				db.exec("ROLLBACK");
				throw err;
			}
		},
	} as unknown as D1Database;
}
