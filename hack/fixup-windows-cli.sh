#!/bin/bash

# replace all .exe.json to .json
FILES=$(find release/public -name "*.exe.json")
for i in $FILES
do
  mv $i ${i/.exe.json/.json}
done

# replace all .exe.gz to .gz
FILES=$(find release/public -name "*.exe.gz")
for i in $FILES
do
  mv $i ${i/.exe.gz/.gz}
done

# replace all .exe to without exe
FILES=$(find release/public -name "*64.exe")
for i in $FILES
do
  mv $i ${i/64.exe/64}
done
