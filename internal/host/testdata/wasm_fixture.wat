(module
  (import "env" "host_add" (func $host_add (param i32 i32) (result i32)))
  (memory (export "memory") 1 10)
  (func (export "add_via_host") (param $a i32) (param $b i32) (result i32)
    local.get $a
    local.get $b
    call $host_add
  )
  (func (export "write_byte") (param $offset i32) (param $value i32)
    local.get $offset
    local.get $value
    i32.store8
  )
  (func (export "read_byte") (param $offset i32) (result i32)
    local.get $offset
    i32.load8_u
  )
  (func (export "grow") (param $delta i32) (result i32)
    local.get $delta
    memory.grow
  )
  (func (export "mem_size") (result i32)
    memory.size
  )
)
