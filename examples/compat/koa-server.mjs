// Real Koa (npm `koa`), unmodified, running on noderati's new
// http.createServer. Koa's own `app.listen(...)` (lib/application.js) is
// `http.createServer(this.callback()).listen(...)` - real Koa middleware
// composition (koa-compose), ctx.body assignment, and its own
// respond()/headersSent handling all run for real here.
import Koa from "koa";

const app = new Koa();

app.use(async (ctx, next) => {
  const start = Date.now();
  await next();
  console.log(`[koa] ${ctx.method} ${ctx.url} -> ${ctx.status} (${Date.now() - start}ms)`);
});

app.use(async (ctx) => {
  if (ctx.path === "/hello") {
    ctx.body = "Hello from Koa on noderati!\n";
    return;
  }
  ctx.status = 404;
  ctx.body = "not found\n";
});

const port = Number(process.env.PORT) || 3001;
app.listen(port, () => {
  console.log(`Koa app listening on http://127.0.0.1:${port}`);
});
