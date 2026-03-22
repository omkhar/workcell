#define _GNU_SOURCE

#include <dlfcn.h>
#include <errno.h>
#include <spawn.h>
#include <stdbool.h>
#include <stddef.h>
#include <string.h>
#include <unistd.h>

static bool is_git_path(const char *path) {
  const char *base;

  if (path == NULL || *path == '\0') {
    return false;
  }

  base = strrchr(path, '/');
  base = (base == NULL) ? path : base + 1;
  return strcmp(base, "git") == 0;
}

static bool should_block(const char *path, char *const argv[]) {
  bool saw_commit = false;
  bool expect_config_arg = false;
  size_t i;

  if (!is_git_path(path) || argv == NULL) {
    return false;
  }

  for (i = 1; argv[i] != NULL; ++i) {
    if (expect_config_arg) {
      expect_config_arg = false;
      if (strncmp(argv[i], "core.hooksPath=", 15) == 0) {
        return true;
      }
      continue;
    }

    if (strcmp(argv[i], "-c") == 0 || strcmp(argv[i], "--config-env") == 0) {
      expect_config_arg = true;
      continue;
    }

    if (strncmp(argv[i], "--config-env=core.hooksPath=", 28) == 0) {
      return true;
    }

    if (strcmp(argv[i], "commit") == 0) {
      saw_commit = true;
    }
    if (strcmp(argv[i], "--no-verify") == 0) {
      return true;
    }
  }

  if (!saw_commit) {
    return false;
  }

  for (i = 1; argv[i] != NULL; ++i) {
    if (strcmp(argv[i], "-n") == 0) {
      return true;
    }
  }

  return false;
}

static void report_block(void) {
  static const char message[] =
      "Workcell blocked git hook bypass: remove --no-verify, git commit -n, or inline core.hooksPath overrides.\n";

  (void)write(STDERR_FILENO, message, sizeof(message) - 1);
  errno = EPERM;
}

int execve(const char *path, char *const argv[], char *const envp[]) {
  static int (*real_execve)(const char *, char *const[], char *const[]) = NULL;

  if (should_block(path, argv)) {
    report_block();
    return -1;
  }

  if (real_execve == NULL) {
    real_execve = dlsym(RTLD_NEXT, "execve");
  }
  return real_execve(path, argv, envp);
}

int execv(const char *path, char *const argv[]) {
  static int (*real_execv)(const char *, char *const[]) = NULL;

  if (should_block(path, argv)) {
    report_block();
    return -1;
  }

  if (real_execv == NULL) {
    real_execv = dlsym(RTLD_NEXT, "execv");
  }
  return real_execv(path, argv);
}

int execvp(const char *file, char *const argv[]) {
  static int (*real_execvp)(const char *, char *const[]) = NULL;

  if (should_block(file, argv)) {
    report_block();
    return -1;
  }

  if (real_execvp == NULL) {
    real_execvp = dlsym(RTLD_NEXT, "execvp");
  }
  return real_execvp(file, argv);
}

int execvpe(const char *file, char *const argv[], char *const envp[]) {
  static int (*real_execvpe)(const char *, char *const[], char *const[]) = NULL;

  if (should_block(file, argv)) {
    report_block();
    return -1;
  }

  if (real_execvpe == NULL) {
    real_execvpe = dlsym(RTLD_NEXT, "execvpe");
  }
  return real_execvpe(file, argv, envp);
}

int posix_spawn(pid_t *pid, const char *path,
                const posix_spawn_file_actions_t *file_actions,
                const posix_spawnattr_t *attrp, char *const argv[],
                char *const envp[]) {
  static int (*real_posix_spawn)(pid_t *, const char *,
                                 const posix_spawn_file_actions_t *,
                                 const posix_spawnattr_t *, char *const[],
                                 char *const[]) = NULL;

  if (should_block(path, argv)) {
    report_block();
    return EPERM;
  }

  if (real_posix_spawn == NULL) {
    real_posix_spawn = dlsym(RTLD_NEXT, "posix_spawn");
  }
  return real_posix_spawn(pid, path, file_actions, attrp, argv, envp);
}

int posix_spawnp(pid_t *pid, const char *file,
                 const posix_spawn_file_actions_t *file_actions,
                 const posix_spawnattr_t *attrp, char *const argv[],
                 char *const envp[]) {
  static int (*real_posix_spawnp)(pid_t *, const char *,
                                  const posix_spawn_file_actions_t *,
                                  const posix_spawnattr_t *, char *const[],
                                  char *const[]) = NULL;

  if (should_block(file, argv)) {
    report_block();
    return EPERM;
  }

  if (real_posix_spawnp == NULL) {
    real_posix_spawnp = dlsym(RTLD_NEXT, "posix_spawnp");
  }
  return real_posix_spawnp(pid, file, file_actions, attrp, argv, envp);
}
