// Real Connect (npm `connect`), unmodified, running on noderati's new
// http.createServer. `app.listen(port, cb)` internally calls
// `http.createServer(this).listen(port, cb)` - connect's own `listen()`
// (see node_modules/connect/index.js) - so this exercises createServer
// through connect's own code path, not a hand-rolled one.
const connect = require("connect");

const app = connect();

app.use("/", function (req, res, next) {
  console.log(`[connect] ${req.method} ${req.url}`);
  next();
});

app.use("/hello", function (req, res) {
  res.setHeader("Content-Type", "text/plain");
  res.end("Hello from Connect on noderati!\n");
});

app.use(function (req, res) {
  res.statusCode = 404;
  res.end("not found\n");
});

const port = Number(process.env.PORT) || 3000;
app.listen(port, () => {
  console.log(`Connect app listening on http://127.0.0.1:${port}`);
});
