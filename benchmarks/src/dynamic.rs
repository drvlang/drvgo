use super::*;
use crate::cli::{Ci, Client, Evaluation};
use anyhow::ensure;
use std::env;

fn string_option(name: &str, value: Option<&str>) -> String {
    value
        .map(|s| format!("{name} = {}; ", nix_string(s)))
        .unwrap_or_default()
}
fn store_option(name: &str, path: Option<&Path>) -> Result<String> {
    Ok(match path {
        Some(p) => format!(
            "{name} = builtins.storePath {}; ",
            quote_path(&p.canonicalize()?)?
        ),
        None => String::new(),
    })
}
pub(crate) fn expr(repo: &Path, source: &Path, package: &str, attrs: &str) -> Result<String> {
    Ok(format!("(import (builtins.toPath {}) {{}}) {{ src = builtins.path {{ path = {}; name = \"source\"; }}; package = {}; {attrs} }}",quote_path(&repo.join("nix/dynamic.nix"))?,quote_path(source)?,nix_string(package)))
}
fn after_options(after: bool, prepare: bool, data: bool, cut: bool) -> String {
    let mut s = String::new();
    if after {
        for (enabled, k) in [
            (prepare, "prepareModules"),
            (data, "exportData"),
            (cut, "earlyCutoff"),
        ] {
            if enabled {
                s.push_str(&format!("{k} = true; "));
            }
        }
    }
    s
}
pub fn ci(a: Ci) -> Result<()> {
    ensure!(
        !a.allow_empty_tests || a.test,
        "--allow-empty-tests requires --test"
    );
    ensure!(
        !a.after_early_cutoff || a.after_export_data,
        "--after-early-cutoff requires --after-export-data"
    );
    let out = empty_dir(a.out.as_deref(), "drvgo-ci-")?;
    let roots = paths(&a.before, &a.after)?;
    let src = a.source.canonicalize()?;
    let nonce = nonce()?;
    let mut flags = argv![
        "--extra-experimental-features",
        "ca-derivations dynamic-derivations recursive-nix",
        "--option",
        "allow-import-from-derivation",
        "false",
        "--max-jobs",
        "4",
        "--cores",
        "2"
    ];
    for pair in a.nix_option.chunks_exact(2) {
        flags.extend(argv!["--option", pair[0], pair[1]]);
    }
    let mut common = string_option("vendorHash", a.vendor_hash.as_deref());
    if let Some(c) = &a.cgo {
        common.push_str(&format!("cgoEnabled = {}; ", c == "1"));
    }
    if a.test {
        common.push_str("test = true; ");
    }
    if a.allow_empty_tests {
        common.push_str("allowEmptyTests = true; ");
    }
    if !a.ldflags.is_empty() {
        common.push_str(&string_option("ldflags", Some(&a.ldflags)));
    }
    if !a.tags.is_empty() {
        common.push_str(&string_option("tags", Some(&a.tags)));
    }
    let mut report = json!({"scope":"Dynamic evaluation and realization with fresh planners and warm package outputs","source":src,"package":a.package,"vendor_hash":a.vendor_hash,"after_module_cache":a.after_module_cache,"module_cache":a.module_cache,"previous_plan":a.previous_plan,"after_previous_plan":a.after_previous_plan,"before_iets":a.before_iets,"after_iets":a.after_iets,"after_prepare_modules":a.after_prepare_modules,"after_export_data":a.after_export_data,"after_early_cutoff":a.after_early_cutoff,"cgo":a.cgo,"test":a.test,"allow_empty_tests":a.allow_empty_tests,"tags":a.tags,"ldflags":a.ldflags,"nix":version()?,"roots":roots,"flags":flags,"nonce":nonce});
    let mut expected = BTreeMap::new();
    for trial in 0..=a.runs {
        for label in alternating(&LABELS, trial) {
            let after = label == "after";
            let mut opts = format!(
                "name = {}; {common}",
                nix_string(&format!("drvgo-ci-{nonce}-{label}-{trial}"))
            );
            opts += &store_option(
                "moduleCache",
                if after {
                    a.after_module_cache
                        .as_deref()
                        .or(a.module_cache.as_deref())
                } else {
                    a.module_cache.as_deref()
                },
            )?;
            opts += &store_option(
                "previousPlan",
                if after {
                    a.after_previous_plan
                        .as_deref()
                        .or(a.previous_plan.as_deref())
                } else {
                    a.previous_plan.as_deref()
                },
            )?;
            opts += &store_option(
                "iets",
                if after {
                    a.after_iets.as_deref()
                } else {
                    a.before_iets.as_deref()
                },
            )?;
            opts += &after_options(
                after,
                a.after_prepare_modules,
                a.after_export_data,
                a.after_early_cutoff,
            );
            let expression = expr(&roots[label], &src, &a.package, &opts)?;
            let mut row = graph_build(
                &expression,
                &flags,
                &log_file(&out, &format!("{label}-{trial}.log"))?,
                false,
            )?;
            let resolved = Path::new(row["result"].as_str().unwrap()).canonicalize()?;
            ensure!(
                expected.entry(label).or_insert_with(|| resolved.clone()) == &resolved,
                "package output changed for {label}"
            );
            row["command_output"] = json!(resolved);
            row["label"] = json!(label);
            row["trial"] = json!(trial);
            row["warmup"] = json!(trial == 0);
            record(&mut report, &out, row)?;
        }
    }
    finish(
        &mut report,
        &out,
        &LABELS,
        "label",
        None,
        &["evaluation", "realization", "total"],
        false,
    )
}
pub fn client(a: Client) -> Result<()> {
    let out = empty_dir(a.out.as_deref(), "drvgo-client-")?;
    let bin = a.iets.canonicalize()?;
    let iets = bin
        .parent()
        .and_then(Path::parent)
        .context("expected a bin/iets executable")?;
    ensure!(
        iets.parent() == Some(Path::new("/nix/store")),
        "--iets must name a Nix store executable"
    );
    let src = a.source.canonicalize()?;
    let repo = match env::var_os("DRVGO_BENCH_REPO") {
        Some(p) => PathBuf::from(p),
        None => PathBuf::from(env!("CARGO_MANIFEST_DIR"))
            .parent()
            .unwrap()
            .to_path_buf(),
    };
    let mut opts = string_option("vendorHash", a.vendor_hash.as_deref());
    opts += &store_option("moduleCache", a.module_cache.as_deref())?;
    if a.nested_iets {
        opts += &store_option("iets", Some(iets))?;
    }
    let config=format!("{}\nextra-experimental-features = nix-command ca-derivations dynamic-derivations recursive-nix\n",env::var("NIX_CONFIG").unwrap_or_default());
    let mut settings = vec![
        argv!["allow-import-from-derivation", "false"],
        argv!["sandbox", "true"],
        argv!["sandbox-fallback", "false"],
        argv!["max-jobs", "4"],
        argv!["cores", "2"],
    ];
    settings.extend(a.nix_option.chunks_exact(2).map(|s| s.to_vec()));
    let nonce = nonce()?;
    let mut report = json!({"scope":"Nix and classic iets CI builds with equal daemon settings and fresh planners","source":src,"package":a.package,"vendor_hash":a.vendor_hash,"module_cache":a.module_cache,"iets":bin,"nested_iets":a.nested_iets,"settings":settings,"nonce":nonce,"nix_version":version()?,"iets_version":run(&args_array![bin,"version"])?});
    let clients = ["nix", "iets-1", "iets-4"];
    let mut expected = None;
    for trial in 0..=a.runs {
        for client in alternating(&clients, trial) {
            let name = format!("drvgo-client-{nonce}-{client}-{trial}");
            let expression = expr(
                &repo,
                &src,
                &a.package,
                &format!("name = {}; {opts}", nix_string(&name)),
            )?;
            let mut cmd = if client == "nix" { vec![] } else { argv![bin] };
            cmd.extend(argv!["nix-build", "--no-out-link", "--store", "daemon"]);
            for pair in &settings {
                cmd.extend(argv!["--option", pair[0], pair[1]]);
            }
            cmd.extend(argv!["-E", expression]);
            let mut c = command(&cmd);
            c.current_dir(&repo)
                .env("NIX_CONFIG", &config)
                .env("NIX_REMOTE", "daemon")
                .env("IETS_NIX_FALLBACK", "never")
                .env("IETS_CACHE", "false")
                .env("IETS_PROGRESS", "false")
                .stderr(log_file(&out, &format!("{client}-{trial}.log"))?);
            if client != "nix" {
                c.env("IETS_WORKERS", &client[5..]);
            }
            let (result, secs) = measured(|| output(&mut c))?;
            let resolved = Path::new(&result).canonicalize()?;
            ensure!(
                expected.get_or_insert_with(|| resolved.clone()) == &resolved,
                "client returned a different output"
            );
            record(
                &mut report,
                &out,
                json!({"client":client,"trial":trial,"warmup":trial==0,"seconds":secs,"result":result,"command_output":resolved,"command":cmd}),
            )?;
        }
    }
    finish(
        &mut report,
        &out,
        &clients,
        "client",
        None,
        &["seconds"],
        true,
    )
}
pub fn evaluation(a: Evaluation) -> Result<()> {
    ensure!(
        !a.after_early_cutoff || a.after_export_data,
        "early cutoff requires export data"
    );
    ensure!(
        a.dynamic_source.is_some()
            || (a.vendor_hash.is_none()
                && a.module_cache.is_none()
                && a.before_iets.is_none()
                && a.after_iets.is_none()),
        "cache, vendor and nested evaluator options require --dynamic-source"
    );
    let src = a
        .dynamic_source
        .as_deref()
        .map(Path::canonicalize)
        .transpose()?;
    let out = empty_dir(a.out.as_deref(), "drvgo-evaluation-")?;
    let roots = paths(&a.before, &a.after)?;
    let iets = a.iets.canonicalize()?;
    let clients = ["nix", "iets-1", "iets-4"];
    let mut cases = vec![];
    for l in LABELS {
        for c in clients {
            cases.push((l, c));
        }
    }
    let mut report = json!({"scope":"Warm evaluation, no builds, tracing cache and fallback disabled","dynamic_source":src,"package":a.package,"vendor_hash":a.vendor_hash,"module_cache":a.module_cache,"before_iets":a.before_iets,"after_iets":a.after_iets,"after_export_data":a.after_export_data,"after_early_cutoff":a.after_early_cutoff,"roots":roots});
    let mut expected = BTreeMap::new();
    for trial in 0..=a.runs {
        for (label, client) in alternating(&cases, trial) {
            let mut opts = after_options(
                label == "after",
                false,
                a.after_export_data,
                a.after_early_cutoff,
            );
            let (expr, features) = if let Some(src) = &src {
                opts += &string_option("vendorHash", a.vendor_hash.as_deref());
                opts += &store_option("moduleCache", a.module_cache.as_deref())?;
                opts += &store_option(
                    "iets",
                    if label == "after" {
                        a.after_iets.as_deref()
                    } else {
                        a.before_iets.as_deref()
                    },
                )?;
                (
                    format!(
                        "({}).drvPath",
                        self::expr(&roots[label], src, &a.package, &opts)?
                    ),
                    "ca-derivations dynamic-derivations recursive-nix",
                )
            } else {
                (
                    format!(
                        "(import (builtins.toPath {}) {{ {opts} }}).default.drvPath",
                        quote_path(&roots[label])?
                    ),
                    "ca-derivations",
                )
            };
            let mut cmd = if client == "nix" {
                argv![
                    "nix-instantiate",
                    "--eval",
                    "--strict",
                    "--raw",
                    "--read-write-mode"
                ]
            } else {
                argv![
                    iets,
                    "eval",
                    "--impure",
                    "--raw",
                    "--no-tracing-cache",
                    "--workers",
                    &client[5..]
                ]
            };
            cmd.extend(argv![
                "--extra-experimental-features",
                features,
                "--option",
                "allow-import-from-derivation",
                "false",
                "-E",
                expr
            ]);
            let mut c = command(&cmd);
            c.env("IETS_NIX_FALLBACK", "never")
                .env("IETS_PROGRESS", "false")
                .env("NIX_REMOTE", "daemon")
                .stderr(log_file(&out, &format!("{label}-{client}-{trial}.log"))?);
            let (value, secs) = measured(|| output(&mut c))?;
            ensure!(
                expected.entry(label).or_insert_with(|| value.clone()) == &value,
                "unstable derivation for {label}/{client}"
            );
            record(
                &mut report,
                &out,
                json!({"label":label,"client":client,"trial":trial,"warmup":trial==0,"seconds":secs,"derivation":value}),
            )?;
        }
    }
    let mut medians = json!({});
    for (label, client) in cases {
        let rows = report["records"].as_array().unwrap();
        medians[label][client] = json!(stats(
            rows.iter()
                .filter(|r| r["label"] == label && r["client"] == client && r["warmup"] == false),
            "seconds"
        )?);
    }
    report["medians"] = medians;
    save(&out.join("results.json"), &report)?;
    pretty(&report["medians"])
}
