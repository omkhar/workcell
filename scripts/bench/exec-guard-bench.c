/* SPDX-License-Identifier: Apache-2.0
 * Copyright 2026 Omkhar Arasaratnam
 *
 * exec-guard-bench: a minimal, dependency-free microbenchmark harness for the
 * workcell_exec_guard LD_PRELOAD shim (runtime/container/rust/src/lib.rs).
 *
 * It times the ALLOW path of one interposed exec/spawn family call against a
 * benign target (default /bin/true) that the guard classifies as "not
 * protected" and forwards to the real libc entry point. Running the same
 * harness twice -- once with LD_PRELOAD pointed at the built cdylib and once
 * without -- isolates the classification overhead the guard adds per call.
 *
 * The harness itself is OS-neutral and compiles on Linux and macOS, but the
 * LD_PRELOAD interposition it measures is Linux-only (the guard reads /proc and
 * hooks the glibc/musl symbols). On macOS only the unhooked baseline is
 * meaningful. See docs/syscall-shim-benchmarks.md.
 *
 * Usage:
 *   exec-guard-bench <mode> <iterations> <warmup> <target>
 *     mode        one of: execve execv execvp posix_spawn
 *     iterations  measured samples (each = one fork+exec+wait / spawn+wait)
 *     warmup      discarded warmup samples run before measurement
 *     target      benign executable to launch (e.g. /bin/true)
 *
 * Output (one line, key=value pairs, all times in nanoseconds):
 *   mode=<mode> n=<count> mean_ns=<m> median_ns=<md> p90_ns=<p> \
 *     stddev_ns=<s> min_ns=<lo> max_ns=<hi>
 *
 * Any failed launch (non-zero child exit or spawn/exec error) aborts with a
 * non-zero status and no numbers, so a blocked exec can never masquerade as a
 * measurement.
 */

/* CLOCK_MONOTONIC/clock_gettime and posix_spawn are POSIX; glibc hides them
 * under strict -std=c11 unless a feature-test macro is set (macOS exposes them
 * by default, and this macro would hide _NSGetEnviron there, so scope it to
 * non-Apple). Must precede every system header. */
#if !defined(__APPLE__)
#define _POSIX_C_SOURCE 200809L
#endif

#include <errno.h>
#include <math.h>
#include <spawn.h>
#include <stdint.h>
#include <stdio.h>
#include <stdlib.h>
#include <string.h>
#include <sys/wait.h>
#include <time.h>
#include <unistd.h>

#if defined(__APPLE__)
#include <crt_externs.h>
#define environ (*_NSGetEnviron())
#else
extern char **environ;
#endif

enum mode {
    MODE_EXECVE,
    MODE_EXECV,
    MODE_EXECVP,
    MODE_POSIX_SPAWN,
};

static int parse_mode(const char *name, enum mode *out) {
    if (strcmp(name, "execve") == 0) {
        *out = MODE_EXECVE;
    } else if (strcmp(name, "execv") == 0) {
        *out = MODE_EXECV;
    } else if (strcmp(name, "execvp") == 0) {
        *out = MODE_EXECVP;
    } else if (strcmp(name, "posix_spawn") == 0) {
        *out = MODE_POSIX_SPAWN;
    } else {
        return -1;
    }
    return 0;
}

static int64_t now_ns(void) {
    struct timespec ts;
    if (clock_gettime(CLOCK_MONOTONIC, &ts) != 0) {
        perror("clock_gettime");
        exit(EXIT_FAILURE);
    }
    return (int64_t)ts.tv_sec * 1000000000LL + (int64_t)ts.tv_nsec;
}

/* Launch the target once via the selected family call and wait for it.
 * Returns 0 on a clean exit(0) child, -1 otherwise. */
static int launch_once(enum mode mode, const char *target, char *const argv[]) {
    if (mode == MODE_POSIX_SPAWN) {
        pid_t pid = 0;
        int rc = posix_spawn(&pid, target, NULL, NULL, argv, environ);
        if (rc != 0) {
            errno = rc;
            perror("posix_spawn");
            return -1;
        }
        int status = 0;
        if (waitpid(pid, &status, 0) < 0) {
            perror("waitpid");
            return -1;
        }
        return (WIFEXITED(status) && WEXITSTATUS(status) == 0) ? 0 : -1;
    }

    pid_t pid = fork();
    if (pid < 0) {
        perror("fork");
        return -1;
    }
    if (pid == 0) {
        switch (mode) {
        case MODE_EXECVE:
            execve(target, argv, environ);
            break;
        case MODE_EXECV:
            execv(target, argv);
            break;
        case MODE_EXECVP:
            execvp(target, argv);
            break;
        default:
            break;
        }
        /* Only reached if exec failed (e.g. a blocked launch). */
        _exit(127);
    }

    int status = 0;
    if (waitpid(pid, &status, 0) < 0) {
        perror("waitpid");
        return -1;
    }
    return (WIFEXITED(status) && WEXITSTATUS(status) == 0) ? 0 : -1;
}

static int cmp_i64(const void *a, const void *b) {
    int64_t x = *(const int64_t *)a;
    int64_t y = *(const int64_t *)b;
    if (x < y) {
        return -1;
    }
    if (x > y) {
        return 1;
    }
    return 0;
}

int main(int argc, char **argv) {
    if (argc != 5) {
        fprintf(stderr,
                "usage: %s <execve|execv|execvp|posix_spawn> <iterations> "
                "<warmup> <target>\n",
                argv[0]);
        return EXIT_FAILURE;
    }

    enum mode mode;
    if (parse_mode(argv[1], &mode) != 0) {
        fprintf(stderr, "unknown mode: %s\n", argv[1]);
        return EXIT_FAILURE;
    }

    char *end = NULL;
    long iterations = strtol(argv[2], &end, 10);
    if (*end != '\0' || iterations <= 0) {
        fprintf(stderr, "invalid iterations: %s\n", argv[2]);
        return EXIT_FAILURE;
    }
    long warmup = strtol(argv[3], &end, 10);
    if (*end != '\0' || warmup < 0) {
        fprintf(stderr, "invalid warmup: %s\n", argv[3]);
        return EXIT_FAILURE;
    }
    const char *target = argv[4];

    /* argv[0] carries the target basename so execvp PATH search (bare names)
     * and absolute launches both present a conventional argv to the guard. */
    const char *base = strrchr(target, '/');
    base = base != NULL ? base + 1 : target;
    char *const child_argv[] = {(char *)base, NULL};

    for (long i = 0; i < warmup; i++) {
        if (launch_once(mode, target, child_argv) != 0) {
            fprintf(stderr, "warmup launch failed for target %s\n", target);
            return EXIT_FAILURE;
        }
    }

    int64_t *samples = calloc((size_t)iterations, sizeof(int64_t));
    if (samples == NULL) {
        perror("calloc");
        return EXIT_FAILURE;
    }

    for (long i = 0; i < iterations; i++) {
        int64_t start = now_ns();
        int rc = launch_once(mode, target, child_argv);
        int64_t elapsed = now_ns() - start;
        if (rc != 0) {
            fprintf(stderr, "measured launch failed for target %s\n", target);
            free(samples);
            return EXIT_FAILURE;
        }
        samples[i] = elapsed;
    }

    long double sum = 0.0L;
    long double sum_sq = 0.0L;
    for (long i = 0; i < iterations; i++) {
        long double v = (long double)samples[i];
        sum += v;
        sum_sq += v * v;
    }
    long double mean = sum / (long double)iterations;
    long double variance = sum_sq / (long double)iterations - mean * mean;
    if (variance < 0.0L) {
        variance = 0.0L;
    }
    long double stddev = sqrtl(variance);

    qsort(samples, (size_t)iterations, sizeof(int64_t), cmp_i64);
    int64_t median = samples[iterations / 2];
    long p90_index = (iterations * 9) / 10;
    if (p90_index >= iterations) {
        p90_index = iterations - 1;
    }
    int64_t p90 = samples[p90_index];
    int64_t min = samples[0];
    int64_t max = samples[iterations - 1];

    printf("mode=%s n=%ld mean_ns=%.0Lf median_ns=%lld p90_ns=%lld "
           "stddev_ns=%.0Lf min_ns=%lld max_ns=%lld\n",
           argv[1], iterations, mean, (long long)median, (long long)p90, stddev,
           (long long)min, (long long)max);

    free(samples);
    return EXIT_SUCCESS;
}
