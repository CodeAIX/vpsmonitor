#!/bin/sh
# Each file gets a fresh Bun process: existing mock.module calls otherwise leak
# into other files, with behavior depending on filesystem enumeration order.
set -eu
for test_file in tests/*.test.ts tests/*.test.tsx; do
  [ -f "$test_file" ] || continue
  bun test --preload ./tests/setup.ts "./$test_file"
done
