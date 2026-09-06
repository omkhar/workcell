use std::env;
use std::path::PathBuf;
use std::process::Command;

fn main() {
    println!("cargo:rerun-if-changed=src/exec_variadic.c");

    if env::var("CARGO_CFG_TARGET_OS").as_deref() != Ok("linux")
        || env::var("HOST").ok() != env::var("TARGET").ok()
    {
        return;
    }

    let out_dir = PathBuf::from(env::var_os("OUT_DIR").expect("Cargo sets OUT_DIR"));
    let object = out_dir.join("exec_variadic.o");
    let source = PathBuf::from("src/exec_variadic.c");
    let compiler = env::var_os("CC").unwrap_or_else(|| "cc".into());
    let status = Command::new(compiler)
        .args([
            "-std=c11",
            "-fPIC",
            "-fvisibility=hidden",
            "-O2",
            "-Wall",
            "-Wextra",
            "-Werror",
            "-DWORKCELL_NO_SYMVER",
            "-c",
        ])
        .arg(&source)
        .arg("-o")
        .arg(&object)
        .status()
        .expect("compile the Linux variadic exec adapter");
    assert!(
        status.success(),
        "Linux variadic exec adapter compilation failed"
    );

    let archive = out_dir.join("libworkcell_exec_variadic.a");
    let archiver = env::var_os("AR").unwrap_or_else(|| "ar".into());
    let status = Command::new(archiver)
        .args(["rcs"])
        .arg(&archive)
        .arg(&object)
        .status()
        .expect("archive the Linux variadic exec adapter");
    assert!(
        status.success(),
        "Linux variadic exec adapter archiving failed"
    );

    // Link the archive into every native target. Unit tests must resolve the
    // exported trampolines as well as the preload shared library.
    println!("cargo:rustc-link-search=native={}", out_dir.display());
    println!("cargo:rustc-link-lib=static=workcell_exec_variadic");
    // Bind adapter calls to the Rust bridges in this shared object. This avoids
    // a caller-controlled interposer replacing the guard's internal dispatch.
    println!("cargo:rustc-link-arg-cdylib=-Wl,-Bsymbolic-functions");
}
