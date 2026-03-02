import { drizzle } from "drizzle-orm/postgres-js";
import postgres from "postgres";
import * as schema from "./schema";

const connectionString = process.env.DATABASE_URL!;

console.log(`[db] DATABASE_URL host: ${connectionString ? new URL(connectionString).host : "NOT SET"}`);

const client = postgres(connectionString);
export const db = drizzle(client, { schema });
