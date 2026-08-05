// This is the generated application's server-side entrypoint used by the E2E.
// It imports the real @kedge/actions-node implementation from the workspace;
// it never receives a Databricks URL, PAT, SQL text, or provider credential.
import { createActionsClient } from '../../../../provider-sdk/actions-node/index.mjs';

const hubURL = String(process.env.KEDGE_HUB_URL ?? '').trim();
const project = String(process.env.KEDGE_PROJECT ?? '').trim();
const alias = String(process.env.KEDGE_ACTION_ALIAS ?? '').trim();
const token = process.env.KEDGE_CALLER_TOKEN ?? '';
const input = JSON.parse(process.env.KEDGE_ACTION_INPUT_JSON ?? '{"limit":2}');
const action = String(process.env.KEDGE_ACTION ?? 'query_table/v1').trim();
const headers = process.env.KEDGE_ACTION_HEADERS_JSON
  ? JSON.parse(process.env.KEDGE_ACTION_HEADERS_JSON)
  : undefined;

const client = createActionsClient({
  baseURL: `${hubURL}/services/providers/app-studio`,
  project,
  token,
  headers,
});
try {
  const result = action === 'query_table/v1'
    ? await client.queryTable(alias, input)
    : await client.integration(alias).invoke(action, input);
  // stdout is the generated app's bounded result contract. Keep it free of
  // configuration and credentials so the suite can assert safe app output.
  process.stdout.write(JSON.stringify(result));
} catch (error) {
  // Keep negative-path evidence deterministic without dumping a stack or
  // request configuration into generated-app output.
  const status = Number.isInteger(error?.status) ? error.status : 0;
  console.error(`${error?.name ?? 'Error'}: status=${status}: ${error?.message ?? String(error)}`);
  process.exitCode = 1;
}
