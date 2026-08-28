import { parse, stringify, escape, unescape } from "querystring";

const q = parse("foo=bar&baz=qux");
console.log("parse:", q.foo, q.baz);
console.log("stringify:", stringify("a", "1", "b", "2"));
console.log("escape roundtrip:", unescape(escape("a b")));
