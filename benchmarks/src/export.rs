use super::*;
use crate::cli::{Build, Export};
use anyhow::ensure;
pub fn build(a: Build) -> Result<()> {
    let out = empty_dir(a.out.as_deref(), "drvgo-rebuild-")?;
    let roots = paths(&a.before, &a.after)?;
    let session = nonce()?;
    let flags = argv!["--no-link", "--impure", "--max-jobs", "4", "--cores", "2"];
    let mut report = json!({"scope":"Force each package recipe with a fresh trial input; warm tools, sources, and dependency outputs. No network transfer.","nix":version()?,"flags":flags,"roots":roots});
    let make = |label: &str, trial: Option<usize>| -> Result<_> {
        let expr = format!(
            "builtins.attrValues (import (builtins.toPath {}) {{}}).units",
            quote_path(&roots[label])?
        );
        let expr = match trial {
            Some(trial) => format!(
                "map (d: builtins.derivation (d.drvAttrs // {{ drvgoTrial = {}; }})) ({expr})",
                nix_string(&format!("{session}-{label}-{trial}"))
            ),
            None => expr,
        };
        let mut cmd = argv!["nix", "build"];
        cmd.extend_from_slice(&flags);
        cmd.extend(argv!["--expr", expr]);
        Ok(cmd)
    };
    for label in LABELS {
        ensure!(
            combined(
                &mut command(&make(label, None)?),
                &log_file(&out, &format!("{label}-prepare.log"))?
            )?
            .success(),
            "preparing {label} failed"
        );
    }
    for trial in 0..=a.runs {
        for label in alternating(&LABELS, trial) {
            let cmd = make(label, Some(trial))?;
            let log = log_file(&out, &format!("{label}-{trial}.log"))?;
            let (s, time) = measured(|| combined(&mut command(&cmd), &log))?;
            ensure!(s.success(), "rebuild failed");
            record(
                &mut report,
                &out,
                json!({"label":label,"trial":trial,"warmup":trial==0,"seconds":time}),
            )?;
        }
    }
    finish(
        &mut report,
        &out,
        &LABELS,
        "label",
        None,
        &["seconds"],
        true,
    )
}
pub fn export(a: Export) -> Result<()> {
    let out = empty_dir(a.out.as_deref(), "drvgo-benchmark-")?;
    let source = a.source.canonicalize()?;
    let bins = paths(&a.before, &a.after)?;
    let mut report = json!({"scope":"export + evaluation only; no realization","source":source,"package":a.package,"tags":a.tags,"ldflags":a.ldflags,"module_proxy":a.module_proxy,"nix":version()?});
    for (label, p) in &bins {
        report["binaries"][*label] = json!({"path":p,"sha256":sha256(&fs::read(p)?)});
    }
    if let Ok(rev) = run(&args_array!["git", "-C", source, "rev-parse", "HEAD"]) {
        report["source_revision"] = json!(rev);
    }
    for t in 0..=a.runs {
        let order = if t == 0 {
            LABELS.to_vec()
        } else {
            alternating(&LABELS, t - 1)
        };
        let trial = if t == 0 { json!("warmup") } else { json!(t) };
        let suffix = if t == 0 {
            "warmup".into()
        } else {
            t.to_string()
        };
        for label in order {
            let graph = out.join(format!("{label}-export"));
            let mut cmd = argv![
                bins[label],
                "export",
                "-C",
                source,
                "-out",
                graph,
                "-tags",
                a.tags,
                "-ldflags",
                a.ldflags
            ];
            if let Some(proxy) = &a.module_proxy {
                cmd.extend(argv!["-module-proxy", proxy]);
            }
            cmd.push(a.package.clone());
            let log = log_file(&out, &format!("{label}-{suffix}.log"))?;
            let start = Instant::now();
            ensure!(
                combined(&mut command(&cmd), &log)?.success(),
                "export failed"
            );
            let exported = Instant::now();
            let drv = logged(
                &args_array!["nix-instantiate", graph, "-A", "default"],
                &log,
            )?;
            let end = Instant::now();
            record(
                &mut report,
                &out,
                json!({"builder":label,"trial":trial,"warmup":t==0,"command":cmd,"export":(exported-start).as_secs_f64(),"evaluation":(end-exported).as_secs_f64(),"total":(end-start).as_secs_f64(),"derivation":drv}),
            )?;
        }
    }
    finish(
        &mut report,
        &out,
        &LABELS,
        "builder",
        None,
        &["export", "evaluation", "total"],
        false,
    )
}
