import { marked, Renderer } from "marked";

const md = `# Title

Some **bold** and *italic* and \`inline code\`.

## Section

A paragraph with a [link](https://example.com) and an ![image](https://example.com/x.png).

- item one
- item two
  - nested item
- item three

1. first
2. second
3. third

> a blockquote
> spanning two lines

\`\`\`js
function hello(name) {
  return "hello " + name;
}
\`\`\`

| a | b | c |
|---|---|---|
| 1 | 2 | 3 |
| 4 | 5 | 6 |

---

The end.
`;

console.log("=== parse() ===");
console.log(marked.parse(md));

console.log("=== lexer() token types ===");
const tokens = marked.lexer(md);
console.log(JSON.stringify(tokens.map((t) => t.type)));

console.log("=== custom renderer ===");
const renderer = new Renderer();
renderer.heading = (text, level) => `<h${level} class="custom">${text}</h${level}>\n`;
console.log(marked.parse("# Custom Heading\n", { renderer }));

console.log("ALL OK");
