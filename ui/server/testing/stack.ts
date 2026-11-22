// A local stand-in for what the BFF talks to: the mock OIDC issuer and a
// stub API that answers /healthz, /api/v1/me and /api/v1/status. For
// `just ui-dev-stack` and the image smoke test; Playwright runs against
// the real API in k3d (IMPL-0005 Phase 14).
//
//   ISSUER_PORT (9400)  the issuer, at http://127.0.0.1:<port>
//   API_PORT    (9401)  the stub API
import { startMockIssuer } from "./mock-issuer";

const issuerPort = Number(process.env.ISSUER_PORT ?? 9400);
const apiPort = Number(process.env.API_PORT ?? 9401);

const issuer = await startMockIssuer({ port: issuerPort });

const me = { subject: "user-1", name: "Grace Hopper", groups: ["platform"], all_owners: true, owners: ["acme-web"], operator: true };

const api = Bun.serve({
  port: apiPort,
  fetch(req) {
    const { pathname } = new URL(req.url);
    if (pathname === "/healthz") return new Response("ok");
    if (pathname === "/api/v1/me") {
      return req.headers.get("authorization") ? Response.json(me) : Response.json({ type: "unauthenticated", title: "Unauthorized", status: 401 }, { status: 401 });
    }
    if (pathname === "/api/v1/status") {
      return Response.json({ overall: "operational", components: [], fleet: { repositories: 0, open_prs: 0, runs_24h: 0 }, version: "dev", computed_at: new Date().toISOString() });
    }
    return Response.json({ type: "not_found", title: "Not Found", status: 404 }, { status: 404 });
  },
});

console.log(JSON.stringify({ msg: "dev stack up", issuer: issuer.url.href, api: `http://127.0.0.1:${api.port}` }));
