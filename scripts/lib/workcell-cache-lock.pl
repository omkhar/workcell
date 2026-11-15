#!/usr/bin/perl
# SPDX-License-Identifier: Apache-2.0
# Copyright 2026 Omkhar Arasaratnam
#
# workcell-cache-lock.pl: the advisory lock that workcell --gc and every Go
# cache writer share on a Workcell cache root (issue 812). The lock is
# flock(2) on PARENT/workcell.lock, beside the root and never inside it, so gc
# never deletes the file that it locks. macOS ships no flock(1), but both
# macOS and Debian ship /usr/bin/perl, whose flock calls flock(2).
#
#   prepare LOCK
#     Create the lock directory and the lock file if they are missing, check
#     them, and print the lock file's path in the directory that LOCK's
#     directory resolves to. The writer's shell then opens that path on a
#     descriptor of its own.
#
#   lock-shared FD LOCK
#     Wait for a shared lock on descriptor FD, which the caller opened on
#     LOCK, the path that prepare printed, and say so on stderr when a gc or
#     an uninstall holds it. flock(2) locks the open file, not this process,
#     so the lock stays after this helper exits, for as long as the caller or
#     any child that inherited the descriptor keeps it open.
#
#   run-exclusive LOCK COMMAND [ARG...]
#     Take an exclusive lock without waiting and exec COMMAND with the lock
#     descriptor open, so the lock lasts exactly as long as COMMAND and its
#     children. Exit 75 when a writer holds the lock.
#
#   check-exclusive LOCK
#     What run-exclusive would exit, for a dry run, without creating LOCK.
#
# Exit 77 means the lock file is unsafe: a symlink, not a regular file, owned
# by another user, linked more than once, open to group or other, or in a
# directory that another user can change, directly or through a symlink that
# another user owns or can replace. A symlinked cache home is fine when only
# this user or root can change the link and the directory it resolves to; an
# unsafe symlink exits 73 instead, since gc may reach the root without it.
# A root run trusts the user who owns the cache home too, and hands a lock
# file or cache home that it creates to that user. A cache root that is a
# symlink takes its lock beside the directory that it points to. A lock file
# or cache home that this user cannot use, such as one that a root run left
# owned by root, is unsafe as well.
# Exit 69 means flock(2) is missing, does not work on the file system, or does
# not lock the open file. A writer that cannot lock for one of these lasting
# reasons goes on, and gc keeps the root, since gc cannot lock it either.
# Exit 75 from prepare or lock-shared means the lock file, or a cache home that
# a root run made, changed as the lock was taken, or that a root run is still
# placing the lock file: a second link to its temporary name, or a file that
# is still root's, made in the last few seconds. The caller tries again. One
# that a killed root run left linked goes; one left as root's is unsafe (77).
# The open never follows a symlink, creates the file owner-only, and never
# changes an existing file. Exit 64, 71 and 73 mean a bad call, a failed
# system call and a lock file that cannot be created.

use strict;
use warnings;
use Config;
use Cwd ();
use Errno qw(EEXIST EACCES ENOENT EPERM);
use Fcntl qw(O_RDONLY O_CREAT O_EXCL O_NOFOLLOW O_NONBLOCK O_DIRECTORY LOCK_SH LOCK_EX LOCK_NB
  F_GETFD F_SETFD FD_CLOEXEC S_ISREG S_ISDIR S_ISLNK S_ISVTX);

use constant {
  EX_USAGE       => 64,
  EX_UNAVAILABLE => 69,
  EX_OSERR       => 71,
  EX_CANTCREAT   => 73,
  EX_TEMPFAIL    => 75,
  EX_NOPERM      => 77,
  MAX_SYMLINKS   => 40,
};

# A refusal once a cache home or root walk has followed a symlink is the
# alias's own: gc of the directory that it points to would not meet it, so it
# stops the run (73) instead of letting the run go on unlocked (77).
our $alias = 0;

sub fail {
  my ($code, $message) = @_;
  ($code, $message) = (EX_CANTCREAT, "$message (through a symlink, so the run stops)") if $alias && $code == EX_NOPERM;
  print STDERR "workcell-cache-lock: $message\n";
  exit $code;
}

# Perl built without flock(2) emulates flock with fcntl locks, which belong
# to the process and vanish when this helper exits or execs.
fail(EX_UNAVAILABLE, 'this perl has no flock(2)') unless ($Config{d_flock} // '') eq 'define';

sub parent_dir {
  my ($path) = @_;
  my $dir = $path;
  $dir =~ s{/+[^/]*\z}{};
  return $dir eq '' ? '/' : $dir;
}

# owned_safely is true for this user and root. A root run, such as sudo
# workcell with the user's HOME, also trusts the user who owns the cache home
# (TRUSTED): only that user and root can change it anyway, and the lock file
# then stays that user's, so the user's own gc and runs share it with root.
sub owned_safely {
  my ($uid, $trusted) = @_;
  return $uid == $> || $uid == 0 || (defined $trusted && $uid == $trusted);
}

# Another user who can change a directory on the way to the lock file can
# swap the file between gc and a writer, so the two would lock different
# files.
sub check_dir {
  my ($what, $dir, $trusted, @st) = @_;
  fail(EX_NOPERM, "refusing $what that is not a directory: $dir") unless S_ISDIR($st[2]);
  fail(EX_NOPERM, "refusing $what owned by uid $st[4]: $dir") unless owned_safely($st[4], $trusted);
  fail(EX_NOPERM, "refusing $what that other users can change (run chmod go-w $dir): $dir")
    if ($st[2] & 022) && !($st[2] & S_ISVTX);
}

# follow_links PATH MISSING_OK OTHERS follows PATH one symlink at a time and
# returns the path that it ends at. Each link must belong to this user or
# root and sit in a directory that no other user can change. A root run
# learns the cache home's owner only at the end, so it notes each other owner
# in OTHERS for lock_target to check. A missing PATH ends the walk when
# MISSING_OK is set, and fails otherwise.
sub follow_links {
  my ($cur, $missing_ok, $others) = @_;
  my $start = $cur;
  local $alias = $alias;
  for (my $hops = 0; ; $hops++) {
    my @st = lstat($cur);
    unless (@st) {
      return $cur if $missing_ok && $!{ENOENT};
      fail(EX_NOPERM, "refusing a symlink loop: $cur") if $!{ELOOP};
      # Such as a cache home that a root run left owner-only: lasting, so a
      # writer warns and goes on instead of stopping on every run.
      fail(EX_NOPERM, "refusing a path that this user cannot reach: $cur") if $!{EACCES};
      fail(EX_CANTCREAT, "cannot inspect $cur: $!");
    }
    return $cur unless S_ISLNK($st[2]);
    $alias = 1;
    fail(EX_NOPERM, 'refusing a path with more than ' . MAX_SYMLINKS . " symlinks: $start")
      if $hops >= MAX_SYMLINKS;
    my $holder = Cwd::realpath(parent_dir($cur));
    defined $holder or fail(EX_CANTCREAT, 'cannot resolve ' . parent_dir($cur) . ": $!");
    my @holder_st = stat($holder) or fail(EX_CANTCREAT, "cannot inspect $holder: $!");
    for my $owned ([$st[4], "symlink owned by uid $st[4]: $cur"],
      [$holder_st[4], "directory that holds a symlink owned by uid $holder_st[4]: $holder"]) {
      next if owned_safely($owned->[0]);
      fail(EX_NOPERM, "refusing $owned->[1]") unless $> == 0;
      $others->{$owned->[0]} = $owned->[1];
    }
    # Its owner was checked above, so this checks its type and mode.
    check_dir('directory that holds a symlink', $holder, $holder_st[4], @holder_st);
    my $link = readlink($cur);
    defined $link or fail(EX_CANTCREAT, "cannot read symlink $cur: $!");
    # A trailing / or /. would make the next lstat follow a link unchecked.
    $link =~ s{(?:/+\.?)+\z}{} unless $link =~ m{\A/+\z};
    $cur = $link =~ m{\A/} ? $link : ($holder eq '/' ? "/$link" : "$holder/$link");
  }
}

# lock_target LOCK returns the lock file for LOCK, the user who must own it,
# and the lock directory's group. A cache root ROOT whose lock is ROOT.lock
# may be a symlink, such as ~/.cache/workcell to ~/Library/Caches/workcell:
# its lock is beside the directory that it points to, the lock that gc of
# that directory takes. The lock directory, a cache home, may be a symlink
# too, a common dotfile layout; the directory that it resolves to must pass
# the same checks. As before, the directories above the cache home are not
# checked; the default cache homes sit in the user's own home.
sub lock_target {
  my ($path) = @_;
  fail(EX_USAGE, "lock path must be absolute: $path") unless $path =~ m{\A/};
  fail(EX_USAGE, "lock path must name a file: $path") if $path =~ m{/\z};
  my %others;
  if ($path =~ m{\A(.+)\.lock\z}) {
    my $named = $1;
    my $home = follow_links(parent_dir($named), 0, \%others);
    $home = Cwd::realpath($home) if -d $home;
    my $root = follow_links($named, 1, \%others);
    # Lock the physical root, which every spelling of it, gc's included,
    # reaches. A missing root's lock is beside its resolved parent below.
    $root = Cwd::realpath($root) // fail(EX_CANTCREAT, "cannot resolve cache root $named: $!") if -d $root;
    $root =~ s{(?:/+\.?)+\z}{};
    # Such as a link to its own cache home, or .../workcell/..: a cache root
    # that resolves to its cache home, or above it, would put its lock beside
    # the home or further up instead of beside a root.
    fail(EX_CANTCREAT, "refusing cache root $named: it resolves to $root, which is not a directory"
      . ' below its cache home, so the run stops')
      unless $root =~ m{/[^/]+\z} && $root !~ m{/\.\.?\z} && !(-d $home && index("$home/", "$root/") == 0);
    $path = "$root.lock";
  }
  (my $base = $path) =~ s{\A.*/}{};
  my $dir = parent_dir($path);
  my $cur = follow_links($dir, 0, \%others);
  my $real = Cwd::realpath($cur);
  defined $real or fail($!{ELOOP} ? EX_NOPERM : EX_CANTCREAT, "cannot resolve $dir: $!");
  my @st = lstat($real) or fail(EX_CANTCREAT, "cannot inspect $real: $!");
  my $trusted = $> == 0 ? $st[4] : undef;
  for my $uid (sort keys %others) {
    fail(EX_CANTCREAT, "refusing $others{$uid} (through a symlink, so the run stops)")
      unless owned_safely($uid, $trusted);
  }
  check_dir('lock directory', $real, $trusted, @st);
  my $owner = defined $trusted ? $trusted : $>;
  return ($real eq '/' ? "/$base" : "$real/$base", $owner, $st[5]);
}

# make_lock_dir DIR creates DIR and each missing directory above it
# owner-only. A root run, such as sudo with the user's HOME, gives each one
# that it creates to the owner of the directory above them, so the user's own
# runs can still lock there. It makes each one under a temporary name of its
# own and renames it into place once it is the user's, so no run of the user's
# meets a root-owned home. The user can swap the temporary name, so root gives
# away only the empty directory that it made: fchown goes through a descriptor
# opened without following a symlink and checked to be root's and empty, and
# the name must still be that directory once renamed (75, retry, if not).
sub make_lock_dir {
  my ($dir) = @_;
  my @missing;
  my $cur = $dir;
  until (lstat($cur)) {
    fail(EX_NOPERM, "refusing a path that this user cannot reach: $cur") if $!{EACCES} || $!{EPERM};
    fail(EX_CANTCREAT, "cannot inspect $cur: $!") unless $!{ENOENT};
    unshift @missing, $cur;
    $cur = parent_dir($cur);
  }
  return unless @missing;
  my @owner = (stat($cur))[4, 5] or fail(EX_CANTCREAT, "cannot inspect $cur: $!");
  my $umask = umask(077);
  for my $new (@missing) {
    my $give = $> == 0 && $owner[0] != 0;
    remove_stale_temps(parent_dir($new)) if $give;
    my $made = $give ? temp_name($new, 'cache-home') : $new;
    unless (mkdir($made, 0700)) {
      next if $!{EEXIST} && !$give;
      fail(EX_CANTCREAT, "cannot create $made: $!");
    }
    next unless $give;
    sysopen(my $dh, $made, O_RDONLY | O_DIRECTORY | O_NOFOLLOW) or fail(EX_TEMPFAIL, "cannot open $made: $!");
    fail(EX_TEMPFAIL, "temporary directory changed as it was made: $made") unless made_empty($dh, $made);
    chown($owner[0], $owner[1], $dh) or fail(EX_OSERR, "cannot give $made to uid $owner[0]: $!");
    if (rename($made, $new)) {
      fail(EX_TEMPFAIL, "cache home changed as it was made: $new") unless same_file($dh, $new);
      next;
    }
    my $error = "$!";
    rmdir($made);
    fail(EX_CANTCREAT, "cannot create $new: $error") unless -d $new;
  }
  umask($umask);
}

# temp_name PATH WHAT names a temporary file or directory beside PATH that no
# other run uses. TEMP_NAME matches such a name and nothing else, so a cache
# root or lock file named like one is never taken for it.
use constant TEMP_NAME => qr/\A\.workcell-(cache-home|lock)\.[0-9]+\.[0-9a-f]{8}\z/;
sub temp_name {
  my ($path, $what) = @_;
  return sprintf('%s/.workcell-%s.%d.%08x', parent_dir($path), $what, $$, int(rand(0xffffffff)));
}

# made_empty is true when DH, opened on PATH, is an empty directory that root
# owns and that PATH still names: the one that this run made.
sub made_empty {
  my ($dh, $path) = @_;
  my @st = stat($dh) or return 0;
  return 0 unless S_ISDIR($st[2]) && $st[4] == 0;
  opendir(my $list, $path) or return 0;
  my @listed = stat($list) or return 0;
  return 0 unless $listed[0] == $st[0] && $listed[1] == $st[1];
  return !grep { $_ ne '.' && $_ ne '..' } readdir($list);
}

# remove_stale_temps DIR removes what root runs killed midway left in DIR,
# once it is over a minute old: an empty temporary cache home that root owns,
# and a temporary lock file that is a regular file. A younger one may be
# another root run's at work, a cache home that anyone else owns is never
# root's to remove, and a symlink stays.
sub remove_stale_temps {
  my ($dir) = @_;
  opendir(my $list, $dir) or return;
  for my $entry (readdir($list)) {
    my ($what) = $entry =~ TEMP_NAME or next;
    my @st = lstat("$dir/$entry");
    next unless @st && $st[9] < time() - 60;
    if ($what eq 'cache-home') {
      rmdir("$dir/$entry") if S_ISDIR($st[2]) && $st[4] == 0;
    } elsif (S_ISREG($st[2])) {
      unlink("$dir/$entry");
    }
  }
}

# fresh is true when CTIME is within the last FRESH_LINK seconds: a root run
# may still be placing that lock file. A ctime in the future is not fresh.
use constant FRESH_LINK => 10;
sub fresh {
  my ($ctime) = @_;
  my $now = time();
  return $ctime > $now - FRESH_LINK && $ctime <= $now;
}

# linked_temp returns the root run's temporary lock file in DIR that is
# another name of the lock file whose stat is ST, or undef.
sub linked_temp {
  my ($dir, @st) = @_;
  opendir(my $list, $dir) or return undef;
  for my $entry (readdir($list)) {
    my ($what) = $entry =~ TEMP_NAME or next;
    my @temp = lstat("$dir/$entry");
    return "$dir/$entry" if $what eq 'lock' && @temp && S_ISREG($temp[2]) && $temp[0] == $st[0] && $temp[1] == $st[1];
  }
  return undef;
}

# create_given makes TARGET, a lock file that a root run creates for OWNER,
# under a temporary name, gives it to OWNER and links it into place. The lock
# file is never root's under its own name, and link(2) fails with EEXIST, as
# an O_EXCL open would, if a run made TARGET meanwhile. A file system without
# link(2), such as some FUSE file systems, gets an O_EXCL create that is given
# away through its descriptor instead. Any other failure to link stops the
# run (73): one that let the run go on unlocked would let gc remove its root.
# It returns the open file, or undef with $! set by the failed open.
sub create_given {
  my ($target, $owner, $group) = @_;
  remove_stale_temps(parent_dir($target));
  my $temp = temp_name($target, 'lock');
  sysopen(my $fh, $temp, O_RDONLY | O_CREAT | O_EXCL | O_NOFOLLOW | O_NONBLOCK, 0600) or return undef;
  give_lock($fh, $target, $owner, $group, $temp);
  my $linked = link($temp, $target);
  my ($errno, $error) = ($! + 0, "$!");
  unlink($temp);
  return $fh if $linked;
  $! = $errno;
  return undef if $errno == EEXIST;
  fail(EX_CANTCREAT, "cannot link lock file $target into place: $error")
    unless $!{EPERM} || $!{ENOTSUP} || $!{EOPNOTSUPP} || $!{EMLINK};
  sysopen($fh, $target, O_RDONLY | O_CREAT | O_EXCL | O_NOFOLLOW | O_NONBLOCK, 0600) or return undef;
  give_lock($fh, $target, $owner, $group);
  return $fh;
}

# give_lock hands the lock file open on FH to OWNER, and stops the run (73)
# when it cannot. It removes TEMP, a temporary name that only this run uses,
# but never TARGET itself, which another run may already have open: that
# root-owned lock file stays, and later runs refuse it as one that a root run
# left (77), so gc keeps the root.
sub give_lock {
  my ($fh, $target, $owner, $group, $temp) = @_;
  return if chown($owner, $group, $fh);
  my $error = "$!";
  unlink($temp) if defined $temp;
  fail(EX_CANTCREAT, "cannot give lock file $target to uid $owner, so the run stops: $error");
}

sub check_lock_file {
  my ($fh, $path, $owner) = @_;
  my @st = stat($fh) or fail(EX_CANTCREAT, "cannot inspect lock file $path: $!");
  fail(EX_NOPERM, "refusing lock file that is not a regular file: $path") unless S_ISREG($st[2]);
  fail(EX_NOPERM, "refusing lock file owned by uid $st[4]: $path") unless $st[4] == $owner;
  if ($st[3] != 1) {
    # A root run links its temporary name into place and removes it next, so
    # a fresh second link is brief (75). One that a killed root run left is
    # another name of this owner's lock file in this owner's directory: it
    # goes, so the run locks instead of refusing the file (77) until a later
    # root run sweeps it.
    if (defined(my $temp = linked_temp(parent_dir($path), @st))) {
      fail(EX_TEMPFAIL, "lock file still linked to a root run's temporary name: $path")
        if fresh($st[10]);
      unlink($temp);
      @st = stat($fh) or fail(EX_CANTCREAT, "cannot inspect lock file $path: $!");
    }
    fail(EX_NOPERM, "refusing lock file with $st[3] links: $path") unless $st[3] == 1;
  }
  fail(EX_NOPERM, sprintf('refusing lock file with mode %04o: %s', $st[2] & 07777, $path))
    if $st[2] & 077;
}

# same_file is true when PATH still names the file open on FH, so a lock on
# FH is the lock that the next process to open PATH meets.
sub same_file {
  my ($fh, $path) = @_;
  my @open = stat($fh) or return 0;
  my @named = lstat($path) or return 0;
  return !S_ISLNK($named[2]) && $open[0] == $named[0] && $open[1] == $named[1];
}

# A dry run checks the lock without creating it (check-exclusive), since no
# writer holds a lock file that does not exist.
my $no_create = 0;

sub open_lock {
  my ($path) = @_;
  my ($target, $owner, $group) = lock_target($path);

  # flock(2) needs no write access, so the descriptor is read-only and the
  # helper never writes a lock file. O_NONBLOCK keeps a FIFO from hanging the
  # open; the regular-file check below refuses it. A root run creates the
  # file for the cache home's owner through create_given.
  my $fh;
  my $created = !$no_create && ($owner == $>
    ? sysopen($fh, $target, O_RDONLY | O_CREAT | O_EXCL | O_NOFOLLOW | O_NONBLOCK, 0600)
    : defined($fh = create_given($target, $owner, $group)));
  unless ($created) {
    # Keep the open's error: the probes below change $!.
    my ($errno, $error) = ($! + 0, "$!");
    if ($no_create || $errno == EEXIST) {
      if (sysopen($fh, $target, O_RDONLY | O_NOFOLLOW | O_NONBLOCK)) {
        $errno = 0;
      } else {
        ($errno, $error) = ($! + 0, "$!");
        exit 0 if $no_create && $errno == ENOENT;
      }
    }
    if ($errno) {
      fail(EX_NOPERM, "refusing symlinked lock file: $target") if -l $target;
      fail(EX_NOPERM, "refusing lock file that is not a regular file: $target") if -e _ && !-f _;
      if ($errno == EACCES || $errno == EPERM) {
        # A root run without link(2) creates the file as root and gives it
        # to this user next, so a fresh root-owned file is brief (75).
        my @named = lstat($target);
        fail(EX_TEMPFAIL, "lock file still root's while a root run gives it to this user: $target")
          if @named && $named[4] == 0 && fresh($named[10]);
        fail(EX_NOPERM, "refusing lock file that this user cannot open, such as one that a root run left: $target");
      }
      fail(EX_CANTCREAT, "cannot open lock file $target: $error");
    }
  }
  check_lock_file($fh, $target, $owner);
  fail(EX_TEMPFAIL, "lock file changed as it was opened: $target") unless same_file($fh, $target);
  return ($fh, $target, $owner);
}

# take_lock returns 0 when a non-blocking lock would wait. A file system
# without working flock(2), such as NFS without a lock manager or some FUSE
# file systems, is a lasting refusal (69), as when perl has no flock(2).
sub take_lock {
  my ($fh, $mode) = @_;
  while (1) {
    return 1 if flock($fh, $mode);
    next if $!{EINTR};
    return 0 if ($mode & LOCK_NB) && ($!{EWOULDBLOCK} || $!{EAGAIN});
    fail(EX_UNAVAILABLE, "flock(2) does not work on this file system: $!")
      if $!{ENOLCK} || $!{EOPNOTSUPP} || $!{ENOTSUP} || $!{EINVAL};
    fail(EX_OSERR, "flock failed: $!");
  }
}

sub prepare {
  my ($path) = @_;
  fail(EX_USAGE, "lock path must be absolute: $path") unless $path =~ m{\A/};
  make_lock_dir(parent_dir($path));
  my (undef, $target) = open_lock($path);
  fail(EX_USAGE, "lock path has a newline: $target") if $target =~ /\n/;
  print "$target\n";
  exit 0;
}

sub lock_shared {
  my ($fd, $path) = @_;
  fail(EX_USAGE, "descriptor must be a number from 3 to 999: $fd")
    unless $fd =~ /\A[1-9][0-9]{0,2}\z/ && $fd >= 3;
  # The caller opened the descriptor by path, so check that the path is still
  # the checked lock file in a safe directory and that the descriptor is it.
  my ($target, $owner) = lock_target($path);
  fail(EX_TEMPFAIL, "lock directory changed: $path") unless $target eq $path;
  open(my $fh, '<&=', $fd) or fail(EX_USAGE, "descriptor $fd is not open: $!");
  check_lock_file($fh, $target, $owner);
  fail(EX_TEMPFAIL, "descriptor $fd is not the lock file: $target") unless same_file($fh, $target);
  unless (take_lock($fh, LOCK_SH | LOCK_NB)) {
    print STDERR "workcell-cache-lock: waiting for workcell --gc or the uninstaller to finish with $target\n";
    take_lock($fh, LOCK_SH);
  }
  # A gc or an uninstall may have removed or replaced the file meanwhile.
  fail(EX_TEMPFAIL, "lock file replaced as the lock was taken: $target") unless same_file($fh, $target);

  # The lock must belong to the open file, which the caller keeps, and not to
  # this process, which exits now. A second open file in this process meets
  # the shared lock only when the lock belongs to the open file.
  if (sysopen(my $probe, $target, O_RDONLY | O_NOFOLLOW | O_NONBLOCK)) {
    fail(EX_UNAVAILABLE, "flock(2) here locks the process, not the open file: $target")
      if flock($probe, LOCK_EX | LOCK_NB);
  }
  exit 0;
}

sub run_exclusive {
  my ($path, @command) = @_;
  fail(EX_USAGE, 'run-exclusive needs a command') unless @command;
  my ($fh, $target) = open_lock($path);
  exit EX_TEMPFAIL unless take_lock($fh, LOCK_EX | LOCK_NB);
  # A file that replaced this one may already be locked by a writer.
  exit EX_TEMPFAIL unless same_file($fh, $target);
  # A lock that belongs to the process, not the open file, excludes nothing
  # here, and writers refuse it too (69), so keep the root.
  if (sysopen(my $probe, $target, O_RDONLY | O_NOFOLLOW | O_NONBLOCK)) {
    fail(EX_UNAVAILABLE, "flock(2) here locks the process, not the open file: $target")
      if flock($probe, LOCK_SH | LOCK_NB);
  }
  my $flags = fcntl($fh, F_GETFD, 0) or fail(EX_OSERR, "fcntl failed: $!");
  fcntl($fh, F_SETFD, $flags & ~FD_CLOEXEC) or fail(EX_OSERR, "fcntl failed: $!");
  { no warnings 'exec'; exec { $command[0] } @command; }
  fail(126, "cannot run $command[0]: $!");
}

my $action = shift(@ARGV) // '';
if ($action eq 'prepare' && @ARGV == 1) {
  prepare(@ARGV);
} elsif ($action eq 'lock-shared' && @ARGV == 2) {
  lock_shared(@ARGV);
} elsif ($action eq 'run-exclusive' && @ARGV >= 2) {
  run_exclusive(@ARGV);
} elsif ($action eq 'check-exclusive' && @ARGV == 1) {
  $no_create = 1;
  run_exclusive(@ARGV, '/bin/true');
}
fail(EX_USAGE, 'usage: workcell-cache-lock.pl prepare LOCK | lock-shared FD LOCK | run-exclusive LOCK COMMAND [ARG...] | check-exclusive LOCK');
