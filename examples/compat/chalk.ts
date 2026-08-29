import chalk from "chalk";

const colored = chalk.green("hello");
if (typeof colored !== "string" || colored.length === 0) {
  throw new Error("chalk did not return a non-empty string");
}
console.log(colored);
console.log("ok chalk");
