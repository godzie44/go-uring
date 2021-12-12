#!/bin/bash

for conn_cnt in 100 500 1000
do
  for msg_len in 128 1024
  do

    echo "run benchmarks with c = $conn_cnt and len = $msg_len"

    RPS_SUM=0

    for attempt in {1..5}
    do
      $1 8080 &
      SRV_PID=$!
      sleep 1s

      OUT=`cargo run -q --manifest-path $2 --release -- --address "127.0.0.1:8080" --number $conn_cnt --duration 60 --length $msg_len`
      RPS=$(echo "${OUT}" | sed -n '/^Speed/ p' | sed -r 's|^([^.]+).*$|\1|; s|^[^0-9]*([0-9]+).*$|\1 |')
      RPS_SUM=$((RPS_SUM + RPS))

      echo "attempt: $attempt, rps: $RPS "

      kill $SRV_PID
      sleep 1s
    done

    RPS_AVG=$((RPS_SUM/5))
    echo "average RPS: $RPS_AVG "

  done
done