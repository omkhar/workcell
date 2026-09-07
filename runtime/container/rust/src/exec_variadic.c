// SPDX-License-Identifier: Apache-2.0
// Copyright 2026 Omkhar Arasaratnam

#include <errno.h>
#include <stdarg.h>
#include <stddef.h>
#include <stdlib.h>

enum { WORKCELL_MAX_EXEC_ELEMENTS = 65536 };

// The guard's exec implementations, defined in src/lib.rs. Linking is static
// into the guard shared object, so these bind without a dynamic lookup.
extern int guarded_execve(const char *path, char *const argv[], char *const envp[]);
extern int guarded_execv(const char *path, char *const argv[]);
extern int guarded_execvp(const char *file, char *const argv[]);

#define WORKCELL_PUBLIC __attribute__((visibility("default")))

// Flattens the NULL-terminated variadic argument list into an argv array. When
// `envp` is non-NULL the caller is execle, so the element after the sentinel is
// also read and stored there.
static int collect_exec_args(const char *arg0, va_list args, char ***result,
                             char *const **envp) {
    size_t count = 1;
    int terminated = 0;
    va_list probe;
    va_copy(probe, args);
    while (count < WORKCELL_MAX_EXEC_ELEMENTS) {
        if (va_arg(probe, char *) == NULL) {
            terminated = 1;
            break;
        }
        count++;
    }
    if (!terminated) {
        va_end(probe);
        errno = E2BIG;
        return -1;
    }
    char *const *child_env = envp != NULL ? va_arg(probe, char *const *) : NULL;
    va_end(probe);

    char **argv = calloc(count + 1, sizeof(*argv));
    if (argv == NULL) {
        errno = ENOMEM;
        return -1;
    }
    argv[0] = (char *)arg0;
    va_list copy;
    va_copy(copy, args);
    for (size_t index = 1; index < count; index++) {
        argv[index] = va_arg(copy, char *);
    }
    va_end(copy);
    argv[count] = NULL;
    *result = argv;
    if (envp != NULL) {
        *envp = child_env;
    }
    return 0;
}

WORKCELL_PUBLIC int workcell_export_execl(const char *path, const char *arg0, ...) {
    va_list args;
    va_start(args, arg0);
    char **argv = NULL;
    int result = collect_exec_args(arg0, args, &argv, NULL);
    va_end(args);
    if (result != 0) {
        return -1;
    }
    result = guarded_execv(path, argv);
    int saved_errno = errno;
    free(argv);
    errno = saved_errno;
    return result;
}

WORKCELL_PUBLIC int workcell_export_execlp(const char *file, const char *arg0, ...) {
    va_list args;
    va_start(args, arg0);
    char **argv = NULL;
    int result = collect_exec_args(arg0, args, &argv, NULL);
    va_end(args);
    if (result != 0) {
        return -1;
    }
    result = guarded_execvp(file, argv);
    int saved_errno = errno;
    free(argv);
    errno = saved_errno;
    return result;
}

WORKCELL_PUBLIC int workcell_export_execle(const char *path, const char *arg0, ...) {
    va_list args;
    va_start(args, arg0);
    char **argv = NULL;
    char *const *envp = NULL;
    int result = collect_exec_args(arg0, args, &argv, &envp);
    va_end(args);
    if (result != 0) {
        return -1;
    }
    result = guarded_execve(path, argv, envp);
    int saved_errno = errno;
    free(argv);
    errno = saved_errno;
    return result;
}
