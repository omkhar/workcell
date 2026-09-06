// SPDX-License-Identifier: Apache-2.0
// Copyright 2026 Omkhar Arasaratnam

#include <errno.h>
#include <stdarg.h>
#include <stddef.h>
#include <stdlib.h>

enum { WORKCELL_MAX_EXEC_ELEMENTS = 65536 };

extern int workcell_execve(const char *path, char *const argv[], char *const envp[]);
extern int workcell_execv(const char *path, char *const argv[]);
extern int workcell_execvp(const char *file, char *const argv[]);
extern int workcell_execvpe(const char *file, char *const argv[], char *const envp[]);
extern int workcell_execveat(int dirfd, const char *path, char *const argv[],
                             char *const envp[], int flags);
extern int workcell_fexecve(int fd, char *const argv[], char *const envp[]);
extern int workcell_posix_spawn(int *pid, const char *path, const void *file_actions,
                                const void *attrp, char *const argv[], char *const envp[]);
extern int workcell_posix_spawnp(int *pid, const char *file, const void *file_actions,
                                 const void *attrp, char *const argv[], char *const envp[]);
extern long workcell_syscall(long number, long arg1, long arg2, long arg3, long arg4,
                             long arg5, long arg6);

#ifndef WORKCELL_NO_SYMVER
#if defined(__aarch64__)
#define WORKCELL_GLIBC_217 "GLIBC_2.17"
#define WORKCELL_GLIBC_234 "GLIBC_2.34"
#elif defined(__x86_64__)
#define WORKCELL_GLIBC_225 "GLIBC_2.2.5"
#define WORKCELL_GLIBC_211 "GLIBC_2.11"
#define WORKCELL_GLIBC_215 "GLIBC_2.15"
#define WORKCELL_GLIBC_234 "GLIBC_2.34"
#else
#error "unsupported Linux architecture for exec symbol versions"
#endif

#define WORKCELL_SYMVER(target, public_name, version) \
    __asm__(".symver " #target "," #public_name "@" version)
#endif

#define WORKCELL_PUBLIC __attribute__((visibility("default")))

static int collect_exec_args(const char *arg0, va_list args, char ***result) {
    size_t count = 1;
    int terminated = 0;
    va_list probe;
    va_copy(probe, args);
    while (count < WORKCELL_MAX_EXEC_ELEMENTS) {
        char *arg = va_arg(probe, char *);
        if (arg == NULL) {
            terminated = 1;
            break;
        }
        count++;
    }
    va_end(probe);
    if (!terminated) {
        errno = E2BIG;
        return -1;
    }

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
    return 0;
}

static int collect_exec_args_and_env(const char *arg0, va_list args, char ***result,
                                     char *const **envp) {
    size_t count = 1;
    int terminated = 0;
    va_list probe;
    va_copy(probe, args);
    while (count < WORKCELL_MAX_EXEC_ELEMENTS) {
        char *arg = va_arg(probe, char *);
        if (arg == NULL) {
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
    char *const *child_env = va_arg(probe, char *const *);
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
    (void)va_arg(copy, char *);
    va_end(copy);
    argv[count] = NULL;
    *result = argv;
    *envp = child_env;
    return 0;
}

WORKCELL_PUBLIC int workcell_export_execl(const char *path, const char *arg0, ...) {
    va_list args;
    va_start(args, arg0);
    char **argv = NULL;
    int result = collect_exec_args(arg0, args, &argv);
    va_end(args);
    if (result != 0) {
        return -1;
    }
    result = workcell_execv(path, argv);
    int saved_errno = errno;
    free(argv);
    errno = saved_errno;
    return result;
}

WORKCELL_PUBLIC int workcell_export_execlp(const char *file, const char *arg0, ...) {
    va_list args;
    va_start(args, arg0);
    char **argv = NULL;
    int result = collect_exec_args(arg0, args, &argv);
    va_end(args);
    if (result != 0) {
        return -1;
    }
    result = workcell_execvp(file, argv);
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
    int result = collect_exec_args_and_env(arg0, args, &argv, &envp);
    va_end(args);
    if (result != 0) {
        return -1;
    }
    result = workcell_execve(path, argv, envp);
    int saved_errno = errno;
    free(argv);
    errno = saved_errno;
    return result;
}

WORKCELL_PUBLIC int workcell_export_execve(const char *path, char *const argv[], char *const envp[]) {
    return workcell_execve(path, argv, envp);
}

WORKCELL_PUBLIC int workcell_export_execv(const char *path, char *const argv[]) {
    return workcell_execv(path, argv);
}

WORKCELL_PUBLIC int workcell_export_execvp(const char *file, char *const argv[]) {
    return workcell_execvp(file, argv);
}

WORKCELL_PUBLIC int workcell_export_execvpe(const char *file, char *const argv[], char *const envp[]) {
    return workcell_execvpe(file, argv, envp);
}

WORKCELL_PUBLIC int workcell_export_execveat(int dirfd, const char *path,
                                             char *const argv[], char *const envp[], int flags) {
    return workcell_execveat(dirfd, path, argv, envp, flags);
}

WORKCELL_PUBLIC int workcell_export_fexecve(int fd, char *const argv[], char *const envp[]) {
    return workcell_fexecve(fd, argv, envp);
}

WORKCELL_PUBLIC int workcell_export_posix_spawn(int *pid, const char *path,
                                                const void *file_actions, const void *attrp,
                                                char *const argv[], char *const envp[]) {
    return workcell_posix_spawn(pid, path, file_actions, attrp, argv, envp);
}

WORKCELL_PUBLIC int workcell_export_posix_spawnp(int *pid, const char *file,
                                                 const void *file_actions, const void *attrp,
                                                 char *const argv[], char *const envp[]) {
    return workcell_posix_spawnp(pid, file, file_actions, attrp, argv, envp);
}

WORKCELL_PUBLIC long workcell_export_syscall(long number, long arg1, long arg2, long arg3,
                                             long arg4, long arg5, long arg6) {
    return workcell_syscall(number, arg1, arg2, arg3, arg4, arg5, arg6);
}

#ifndef WORKCELL_NO_SYMVER
#if defined(__aarch64__)
WORKCELL_SYMVER(workcell_export_execve, execve, WORKCELL_GLIBC_217);
WORKCELL_SYMVER(workcell_export_execv, execv, WORKCELL_GLIBC_217);
WORKCELL_SYMVER(workcell_export_execvp, execvp, WORKCELL_GLIBC_217);
WORKCELL_SYMVER(workcell_export_execvpe, execvpe, WORKCELL_GLIBC_217);
WORKCELL_SYMVER(workcell_export_execl, execl, WORKCELL_GLIBC_217);
WORKCELL_SYMVER(workcell_export_execlp, execlp, WORKCELL_GLIBC_217);
WORKCELL_SYMVER(workcell_export_execle, execle, WORKCELL_GLIBC_217);
WORKCELL_SYMVER(workcell_export_fexecve, fexecve, WORKCELL_GLIBC_217);
WORKCELL_SYMVER(workcell_export_posix_spawn, posix_spawn, WORKCELL_GLIBC_217);
WORKCELL_SYMVER(workcell_export_posix_spawnp, posix_spawnp, WORKCELL_GLIBC_217);
WORKCELL_SYMVER(workcell_export_syscall, syscall, WORKCELL_GLIBC_217);
#else
WORKCELL_SYMVER(workcell_export_execve, execve, WORKCELL_GLIBC_225);
WORKCELL_SYMVER(workcell_export_execv, execv, WORKCELL_GLIBC_225);
WORKCELL_SYMVER(workcell_export_execvp, execvp, WORKCELL_GLIBC_225);
WORKCELL_SYMVER(workcell_export_execvpe, execvpe, WORKCELL_GLIBC_211);
WORKCELL_SYMVER(workcell_export_execl, execl, WORKCELL_GLIBC_225);
WORKCELL_SYMVER(workcell_export_execlp, execlp, WORKCELL_GLIBC_225);
WORKCELL_SYMVER(workcell_export_execle, execle, WORKCELL_GLIBC_225);
WORKCELL_SYMVER(workcell_export_fexecve, fexecve, WORKCELL_GLIBC_225);
WORKCELL_SYMVER(workcell_export_posix_spawn, posix_spawn, WORKCELL_GLIBC_215);
WORKCELL_SYMVER(workcell_export_posix_spawnp, posix_spawnp, WORKCELL_GLIBC_215);
WORKCELL_SYMVER(workcell_export_syscall, syscall, WORKCELL_GLIBC_225);
#endif
WORKCELL_SYMVER(workcell_export_execveat, execveat, WORKCELL_GLIBC_234);
#endif
