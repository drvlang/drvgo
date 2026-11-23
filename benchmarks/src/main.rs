mod cli;
mod dynamic;
mod export;
mod fixtures;
mod toolchain;
use anyhow::{Context, Result};
use clap::Parser;
use cli::{Cli, Suite};
use drvbench_support::*;
use serde_json::{json, Value};
use std::{
    collections::{BTreeMap, BTreeSet},
    fs,
    path::{Path, PathBuf},
    sync::OnceLock,
    time::Instant,
};

const LABELS: [&str; 2] = ["before", "after"];
fn paths(b: &Path, a: &Path) -> Result<BTreeMap<&'static str, PathBuf>> {
    Ok(BTreeMap::from([
        ("before", b.canonicalize()?),
        ("after", a.canonicalize()?),
    ]))
}
fn version() -> Result<String> {
    run(&args_array!["nix", "--version"])
}
fn write(out: &Path, p: &str, content: &str) -> Result<()> {
    let dest = out.join(p);
    if let Some(parent) = dest.parent() {
        fs::create_dir_all(parent)?;
    }
    fs::write(dest, content)?;
    Ok(())
}
fn record(report: &mut Value, out: &Path, row: Value) -> Result<()> {
    if report.get("records").is_none() {
        report["records"] = json!([]);
    }
    report["records"]
        .as_array_mut()
        .context("records")?
        .push(row);
    save(&out.join("results.json"), report)
}
fn finish(
    report: &mut Value,
    out: &Path,
    labels: &[&str],
    key: &str,
    nested: Option<&str>,
    fields: &[&str],
    scalar: bool,
) -> Result<()> {
    let mut summary = json!({});
    let rows = report["records"].as_array().context("records")?;
    for lab in labels {
        for field in fields {
            let values = rows
                .iter()
                .filter(|r| r[key] == *lab && r["warmup"] != true)
                .map(|r| if let Some(n) = nested { &r[n] } else { r });
            let val = json!(stats(values, field)?);
            if scalar {
                summary[lab] = val;
            } else {
                summary[lab][field] = val;
            }
        }
    }
    pretty(&summary)?;
    report["medians"] = summary;
    save(&out.join("results.json"), report)
}
fn flags(dynamic: bool) -> Vec<String> {
    let mut opts = if dynamic {
        argv![
            "--extra-experimental-features",
            "ca-derivations dynamic-derivations recursive-nix"
        ]
    } else {
        vec![]
    };
    opts.extend(argv![
        "--option",
        "allow-import-from-derivation",
        "false",
        "--option",
        "sandbox",
        "true",
        "--option",
        "sandbox-fallback",
        "false",
        "--option",
        "substituters",
        "",
        "--option",
        "always-allow-substitutes",
        "false",
        "--max-jobs",
        "4",
        "--cores",
        "2"
    ]);
    opts
}
fn graph_build(expr: &str, opts: &[String], log: &fs::File, ca: bool) -> Result<Value> {
    let mut instantiate = argv!["nix-instantiate"];
    instantiate.extend_from_slice(opts);
    instantiate.extend(argv!["--expr", expr]);
    let start = Instant::now();
    let drv = logged(&instantiate, log)?;
    let eval = Instant::now();
    let mut cmd = if ca {
        argv!["nix", "build"]
    } else {
        argv!["nix-store"]
    };
    cmd.extend_from_slice(opts);
    if ca {
        cmd.extend(argv!["--no-link", "--json", format!("{drv}^out")]);
    } else {
        cmd.extend(argv!["--realise", drv]);
    }
    let result = logged(&cmd, log)?;
    let end = Instant::now();
    let output = if ca {
        let v: Value = serde_json::from_str(&result)?;
        v[0]["outputs"]["out"]
            .as_str()
            .context("out path")?
            .to_owned()
    } else {
        result
    };
    Ok(
        json!({"derivation":drv,"result":output,"evaluation":(eval-start).as_secs_f64(),"realization":(end-eval).as_secs_f64(),"total":(end-start).as_secs_f64()}),
    )
}
fn package_builds(log: &Path) -> Result<BTreeSet<String>> {
    static RE: OnceLock<regex::Regex> = OnceLock::new();
    let re = RE.get_or_init(|| {
        regex::Regex::new(r"building '(/nix/store/[^'\n]+-go-[^'\n]+\.drv)'").unwrap()
    });
    Ok(re
        .captures_iter(&fs::read_to_string(log)?)
        .map(|c| c[1].to_owned())
        .collect())
}
fn main() -> Result<()> {
    match Cli::parse().command {
        Suite::Bootstrap(a) => toolchain::bootstrap(a),
        Suite::Build(a) => export::build(a),
        Suite::Ci(a) => dynamic::ci(a),
        Suite::Client(a) => dynamic::client(a),
        Suite::Closure(a) => toolchain::closure(a),
        Suite::Evaluation(a) => dynamic::evaluation(a),
        Suite::ExportData(a) => fixtures::private_edit(a),
        Suite::Export(a) => export::export(a),
        Suite::Pack(a) => toolchain::pack(a),
        Suite::PlannerClosure(a) => toolchain::planner_closure(a),
        Suite::Relocation(a) => fixtures::relocation(a),
        Suite::SourceEdit(a) => fixtures::source_edit(a),
    }
}
