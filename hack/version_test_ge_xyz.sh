#!/bin/sh

# SPDX-FileCopyrightText: The RamenDR authors
# SPDX-License-Identifier: Apache-2.0

IFS=. read -r x y z <<-a
$1
a
IFS=. read -r xx yy zz <<-a
$2
a
printf "version: %s.%s.%s\nminimum: %s.%s.%s\n" "$x" "$y" "$z" "$xx" "$yy" "$zz" >&2
test "$x" -gt "$xx" ||\
{ test "$x" -eq "$xx" &&\
        { test "$y" -gt "$yy" ||\
        { test "$y" -eq "$yy" &&\
                 test "$z" -ge "$zz";};};}
set -- $?
unset -v x y z xx yy zz
exit "$1"
