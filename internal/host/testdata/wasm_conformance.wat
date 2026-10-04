;; Fixture for WebAssembly JS-API conformance beyond the MVP:
;; tail calls, exception handling (try_table), and a multi-value import.
;; Build: wasm-tools parse wasm_conformance.wat -o wasm_conformance.wasm
(module
  (import "env" "size" (func $size (result i32 i32)))
  (tag $boom (param i32))

  ;; Tail-recursive sum 1..n; without return_call this depth would
  ;; exhaust the stack.
  (func $sum_acc (param $n i32) (param $acc i64) (result i64)
    (if (result i64) (i32.eqz (local.get $n))
      (then (local.get $acc))
      (else
        (return_call $sum_acc
          (i32.sub (local.get $n) (i32.const 1))
          (i64.add (local.get $acc) (i64.extend_i32_u (local.get $n)))))))
  (func (export "sum") (param $n i32) (result i64)
    (return_call $sum_acc (local.get $n) (i64.const 0)))

  ;; Throws $boom with the argument and catches it in the same function.
  (func $thrower (param $v i32)
    (throw $boom (local.get $v)))
  (func (export "catch_payload") (param $v i32) (result i32)
    (block $caught (result i32)
      (try_table (catch $boom $caught)
        (call $thrower (local.get $v)))
      (i32.const -1)))

  ;; Calls the two-result import and packs it as cols * 1000 + rows.
  (func (export "packed_size") (result i32)
    (local $rows i32)
    (call $size)
    (local.set $rows)
    (i32.mul (i32.const 1000))
    (local.get $rows)
    (i32.add)))
