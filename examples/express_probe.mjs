import express from "express";

const app = express();
app.use(express.json());

app.get("/", (req, res) => {
  res.send("hello from express");
});

app.get("/users/:id", (req, res) => {
  res.json({ id: req.params.id, query: req.query });
});

app.post("/echo", (req, res) => {
  res.json({ received: req.body });
});

app.use((req, res) => {
  res.status(404).json({ error: "not found" });
});

const server = app.listen(3123, () => {
  console.log("express listening on 3123");
});
