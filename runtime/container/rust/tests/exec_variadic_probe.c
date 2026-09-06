#define _GNU_SOURCE

// SPDX-License-Identifier: Apache-2.0
// Copyright 2026 Omkhar Arasaratnam

#include <errno.h>
#include <stdio.h>
#include <stdlib.h>
#include <string.h>
#include <unistd.h>

static int expect_block(const char *name, int result) {
    if (result == 0) {
        fprintf(stderr, "%s executed the mutable target\n", name);
        return 1;
    }
    return errno == EPERM ? 1 : 2;
}

int main(int argc, char **argv) {
    if (argc != 3) {
        return 2;
    }
    const char *name = argv[1];
    const char *target = argv[2];
    if (strcmp(name, "execl") == 0) {
        return expect_block(name, execl(target, target, (char *)NULL));
    }
    if (strcmp(name, "execlp") == 0) {
        if (setenv("PATH", "/tmp", 1) != 0) {
            return 2;
        }
        return expect_block(name, execlp("workcell-variadic-native", "workcell-variadic-native", (char *)NULL));
    }
    if (strcmp(name, "execle") == 0) {
        char *child_env[] = {
            "LD_PRELOAD=/usr/local/lib/libworkcell_exec_guard.so",
            NULL,
        };
        return expect_block(name, execle(target, target, (char *)NULL, child_env));
    }
    return 2;
}
