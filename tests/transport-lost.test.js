// When the Go process dies, requests must fail at once with the error a caller rebuilds on, and a
// new init must get a working process. Before this, every request waited out its own timeout
// until the host process restarted (2026-10-31: a dev API sat in "maintenance" for minutes).
const http = require("http");
const { execSync } = require("child_process");
const initCycleTLS = require("../dist/index.js");

jest.setTimeout(30000);

const randomPort = () => 20000 + Math.floor(Math.random() * 20000);

/** The process listening on the port: the Go server, not the shell CycleTLS spawns it through. */
function killServerOn(port) {
  const pid = execSync(`lsof -ti tcp:${port} -sTCP:LISTEN`).toString().trim().split("\n")[0];
  process.kill(Number(pid), "SIGKILL");
}

let target;
let targetUrl;

beforeAll(async () => {
  target = http.createServer((req, res) => {
    const delay = req.url === "/slow" ? 5000 : 0;
    setTimeout(() => res.end("ok"), delay);
  });
  await new Promise((resolve) => target.listen(0, "127.0.0.1", resolve));
  targetUrl = `http://127.0.0.1:${target.address().port}`;
});

afterAll(async () => {
  target.closeAllConnections();
  await new Promise((resolve) => target.close(resolve));
});

test("a request in flight fails at once when the Go process dies, and a new init works", async () => {
  const port = randomPort();
  const client = await initCycleTLS({ port });
  expect((await client.get(`${targetUrl}/fast`, { timeout: 10 })).status).toBe(200);

  const inFlight = client.get(`${targetUrl}/slow`, { timeout: 10 });
  const started = Date.now();
  setTimeout(() => killServerOn(port), 300);

  await expect(inFlight).rejects.toThrow("WebSocket server not connected");
  expect(Date.now() - started).toBeLessThan(2000);

  // The old client's next request does not wait either.
  await expect(client.get(`${targetUrl}/fast`, { timeout: 10 })).rejects.toThrow("WebSocket server not connected");
  await client.exit();

  // The lost instance left the manager, so the same port gets a fresh process.
  const fresh = await initCycleTLS({ port });
  expect((await fresh.get(`${targetUrl}/fast`, { timeout: 10 })).status).toBe(200);
  await fresh.exit();
});

test("the Go server listens on loopback only", async () => {
  const port = randomPort();
  const client = await initCycleTLS({ port });
  const listening = execSync(`lsof -nP -iTCP:${port} -sTCP:LISTEN`).toString();
  expect(listening).toContain(`127.0.0.1:${port}`);
  expect(listening).not.toContain(`*:${port}`);
  await client.exit();
});
