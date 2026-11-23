use super::*;
use crate::cli::{Bootstrap, Closure, Pack, PlannerClosure};
use anyhow::{bail, ensure};
use std::process::Command;
fn sizes(out: &Path, roots: &BTreeSet<String>) -> Result<BTreeMap<String, (u64, u64)>> {
    let cache = out.join("cache");
    let mut uri =
        url::Url::from_directory_path(&cache).map_err(|_| anyhow::anyhow!("invalid cache path"))?;
    uri.set_query(Some("compression=zstd&compression-level=3"));
    let mut cmd = argv!["nix", "copy", "--to", uri.as_str()];
    cmd.extend(roots.iter().cloned());
    ensure!(
        combined(&mut command(&cmd), &log_file(out, "copy.log")?)?.success(),
        "copy to cache failed"
    );
    let mut sizes = BTreeMap::new();
    for entry in cache.read_dir()? {
        let p = entry?.path();
        if p.extension().is_none_or(|ext| ext != "narinfo") {
            continue;
        }
        let text = fs::read_to_string(p)?;
        let fields: BTreeMap<_, _> = text.lines().filter_map(|s| s.split_once(": ")).collect();
        sizes.insert(
            fields.get("StorePath").context("StorePath")?.to_string(),
            (
                fields.get("NarSize").context("NarSize")?.parse()?,
                fields.get("FileSize").context("FileSize")?.parse()?,
            ),
        );
    }
    Ok(sizes)
}
fn closure_sizes(roots: &[String], sizes: &BTreeMap<String, (u64, u64)>) -> Result<Value> {
    let mut args = argv!["nix-store", "--query", "--requisites"];
    args.extend_from_slice(roots);
    let text = run(&args)?;
    let paths = text.lines().collect::<BTreeSet<_>>();
    let mut nar = 0;
    let mut compressed = 0;
    for p in &paths {
        let (n, c) = sizes.get(*p).context("missing cached NAR")?;
        nar += n;
        compressed += c;
    }
    Ok(json!({"paths":paths.len(),"nar_bytes":nar,"zstd_bytes":compressed}))
}
pub fn closure(a: Closure) -> Result<()> {
    let out = empty_dir(a.out.as_deref(), "drvgo-closure-")?;
    let mut roles = BTreeMap::new();
    let mut all = BTreeSet::new();
    for (label, path) in paths(&a.before, &a.after)? {
        let mut values = BTreeMap::from([("planning", path.to_str().context("path")?.to_owned())]);
        for role in ["compile", "link", "native", "internal-link"] {
            let p = path.join(format!("nix-support/drvgo-{role}-toolchain"));
            let v = if p.exists() {
                fs::read_to_string(p)?.trim().to_string()
            } else {
                values[if role == "internal-link" {
                    "native"
                } else {
                    "planning"
                }]
                .clone()
            };
            values.insert(role, v);
        }
        all.extend(values.values().cloned());
        roles.insert(label, values);
    }
    let s = sizes(&out, &all)?;
    let mut report =
        json!({"scope":"Complete closures in a local zstd level 3 binary cache","nix":version()?});
    for (label, roles) in roles {
        for (role, p) in roles {
            let mut value = closure_sizes(std::slice::from_ref(&p), &s)?;
            value["path"] = json!(p);
            report["toolchains"][label][role] = value;
        }
    }
    save(&out.join("results.json"), &report)?;
    pretty(&report)
}
pub fn planner_closure(a: PlannerClosure) -> Result<()> {
    let out = empty_dir(a.out.as_deref(), "drvgo-planner-closure-")?;
    let mut all = BTreeSet::new();
    let mut plans = BTreeMap::new();
    let mut report = json!({"scope":"Planner input closures, including raw derivation metadata; local zstd level 3 cache; no network timing","nix":version()?});
    for (label, p) in [("before", &a.before), ("after", &a.after)] {
        let data: Value =
            serde_json::from_str(&run(&args_array!["nix", "derivation", "show", p])?)?;
        let derivs = data
            .get("derivations")
            .unwrap_or(&data)
            .as_object()
            .context("derivation map")?;
        ensure!(derivs.len() == 1, "expected one planner");
        let drv = derivs.values().next().unwrap();
        let new = drv.get("inputs");
        let src = new.map(|v| &v["srcs"]).unwrap_or(&drv["inputSrcs"]);
        let deps = new.map(|v| &v["drvs"]).unwrap_or(&drv["inputDrvs"]);
        let store = Path::new(p).parent().context("store directory")?;
        let mut roots = BTreeSet::new();
        for s in src.as_array().context("sources")? {
            roots.insert(
                store
                    .join(s.as_str().context("source")?)
                    .display()
                    .to_string(),
            );
        }
        let mut cmd = argv!["nix-store", "--realise"];
        for (d, v) in deps.as_object().context("input derivations")? {
            if let Some(dyns) = v.get("dynamicOutputs") {
                ensure!(
                    dyns.is_null() || dyns.as_object().is_some_and(|m| m.is_empty()),
                    "planner has dynamic inputs"
                );
            }
            let names: Vec<_> = v
                .get("outputs")
                .unwrap_or(v)
                .as_array()
                .context("outputs")?
                .iter()
                .map(|x| x.as_str().context("output name"))
                .collect::<Result<_>>()?;
            cmd.push(format!("{}!{}", store.join(d).display(), names.join(",")));
        }
        if cmd.len() > 2 {
            roots.extend(
                logged(&cmd, &log_file(&out, &format!("{label}-inputs.log"))?)?
                    .lines()
                    .map(str::to_string),
            );
        }
        all.extend(roots.iter().cloned());
        plans.insert(label, (p, roots.into_iter().collect::<Vec<_>>()));
    }
    let s = sizes(&out, &all)?;
    for (l, (p, roots)) in plans {
        let mut v = closure_sizes(&roots, &s)?;
        v["derivation"] = json!(p);
        v["roots"] = json!(roots);
        report["planners"][l] = v;
    }
    save(&out.join("results.json"), &report)?;
    pretty(&report)
}
pub fn pack(a: Pack) -> Result<()> {
    let out = empty_dir(a.out.as_deref(), "drvgo-pack-")?;
    let roots = paths(&a.before, &a.after)?;
    let goenv = [
        ("GOENV", "off"),
        ("GOFLAGS", ""),
        ("GOTOOLCHAIN", "local"),
        ("CGO_ENABLED", "0"),
        ("GOPROXY", "off"),
    ];
    let go = |root: &Path| {
        let mut c = Command::new(root.join("bin/go"));
        c.envs(goenv);
        c
    };
    let v = output(go(&roots["before"]).args(["env", "GOVERSION"]))?;
    ensure!(
        v == output(go(&roots["after"]).args(["env", "GOVERSION"]))?,
        "toolchains need the same Go version"
    );
    let env = output(go(&roots["before"]).args(["env", "GOROOT", "GOARCH"]))?;
    let (sdk, arch) = env.split_once('\n').context("Go environment")?;
    let asm = match arch {
        "amd64" => "MOVQ $42, ret+0(FP)\n RET",
        "arm64" => "MOVD $42, R0\n MOVD R0, ret+0(FP)\n RET",
        _ => bail!("unsupported architecture {arch}"),
    };
    write(&out, "sample.go", "package sample\nfunc Answer() uint64\n")?;
    write(
        &out,
        "sample.s",
        &format!("#include \"textflag.h\"\nTEXT ·Answer(SB), NOSPLIT, $0-8\n {asm}\n"),
    )?;
    output(go(&roots["before"]).args(argv![
        "tool",
        "compile",
        "-pack",
        "-p",
        "sample",
        "-o",
        out.join("base.a"),
        out.join("sample.go")
    ]))?;
    output(go(&roots["before"]).args(argv![
        "tool",
        "asm",
        "-p",
        "sample",
        "-I",
        Path::new(sdk).join("pkg/include"),
        "-o",
        out.join("sample.o"),
        out.join("sample.s")
    ]))?;
    let shell = |p: PathBuf| format!("'{}'", p.to_string_lossy().replace('\'', "'\"'\"'"));
    write(
        &out,
        "script",
        &format!(
            "cp {} @SELF@/_pkg_.a\nlibnix-pack r @SELF@/_pkg_.a {}\n",
            shell(out.join("base.a")),
            shell(out.join("sample.o"))
        ),
    )?;
    let mut report = json!({"scope":"One archive append per worker; warm tools and inputs","go":v,"arch":arch,"iterations":a.iterations,"toolchains":roots});
    let mut expected = None;
    for trial in 0..=a.runs {
        for label in alternating(&LABELS, trial) {
            let tc = &roots[label];
            let work = out.join(format!("{label}-{trial}"));
            fs::create_dir(&work)?;
            let mut cmd = Command::new(tc.join("bin/drvgo-builder"));
            cmd.envs(goenv)
                .env("PATH", tc.join("bin"))
                .env("TMPDIR", &work)
                .env("out", work.join("out"))
                .env("scriptPath", out.join("script"))
                .env("NIX_BUILD_CORES", "1")
                .env("isBinary", "0")
                .env("emptyTests", "0")
                .env("GOROOT", tc.join("share/go"))
                .env("toolchainInput", tc);
            let log = log_file(&out, &format!("{label}-{trial}.log"))?;
            let start = Instant::now();
            for _ in 0..a.iterations {
                ensure!(combined(&mut cmd, &log)?.success(), "archive worker failed");
            }
            let elapsed = start.elapsed().as_secs_f64();
            let digest = sha256(&fs::read(work.join("out/_pkg_.a"))?);
            ensure!(
                expected.get_or_insert_with(|| digest.clone()) == &digest,
                "archive bytes changed"
            );
            record(
                &mut report,
                &out,
                json!({"label":label,"trial":trial,"warmup":trial==0,"seconds":elapsed,"seconds_per_archive":elapsed/a.iterations as f64,"sha256":digest}),
            )?;
        }
    }
    finish(
        &mut report,
        &out,
        &LABELS,
        "label",
        None,
        &["seconds_per_archive"],
        true,
    )
}
pub fn bootstrap(a: Bootstrap) -> Result<()> {
    let out = empty_dir(a.out.as_deref(), "drvgo-bootstrap-")?;
    let roots = paths(&a.before, &a.after)?;
    let pkgs = a.nixpkgs.canonicalize()?;
    let nonce = nonce()?;
    let flags = argv![
        "--cores",
        a.cores.to_string(),
        "--max-jobs",
        a.jobs.to_string(),
        "--option",
        "sandbox",
        "true",
        "--option",
        "sandbox-fallback",
        "false",
        "--option",
        "allow-import-from-derivation",
        "false",
        "--option",
        "substituters",
        "",
        "--option",
        "always-allow-substitutes",
        "false"
    ];
    let mut report = json!({"scope":"Fresh CLI package builds with tests and vet; one warmup, warm SDK, no substitutions","roots":roots,"nixpkgs":pkgs,"flags":flags,"nonce":nonce,"nix":version()?});
    for (label, p) in &roots {
        report["revisions"][*label] =
            json!(run(&args_array!["git", "-C", p, "rev-parse", "HEAD"])?);
    }
    for trial in 0..=a.runs {
        for label in alternating(&LABELS, trial) {
            let name = format!("drvgo-bootstrap-{nonce}-{label}-{trial}");
            let expr=format!("let pkgs = import (builtins.toPath {}) {{}}; in (import (builtins.toPath {}) {{ inherit pkgs; }}).overrideAttrs (_: {{ name = {}; }})",quote_path(&pkgs)?,quote_path(&roots[label].join("nix/package.nix"))?,nix_string(&name));
            let file = out.join(format!("{label}-{trial}.nix"));
            fs::write(&file, &expr)?;
            let log = log_file(&out, &format!("{label}-{trial}.log"))?;
            let mut instantiate = argv!["nix-instantiate", file];
            instantiate.extend_from_slice(&flags);
            let start = Instant::now();
            let drv = logged(&instantiate, &log)?;
            let evaluated = Instant::now();
            let mut realize = argv!["nix-store", "--realise", drv];
            realize.extend_from_slice(&flags);
            let output = logged(&realize, &log)?;
            let end = Instant::now();
            let refs = run(&args_array!["nix-store", "--query", "--references", output])?;
            record(
                &mut report,
                &out,
                json!({"label":label,"trial":trial,"warmup":trial==0,"evaluation":(evaluated-start).as_secs_f64(),"realization":(end-evaluated).as_secs_f64(),"total":(end-start).as_secs_f64(),"instantiate":instantiate,"realize":realize,"expression":expr,"derivation":drv,"output":output,"binary_sha256":sha256(&fs::read(Path::new(&output).join("bin/drvgo"))?),"nar_hash":run(&args_array!["nix-hash","--type","sha256",output])?,"references":refs.lines().collect::<BTreeSet<_>>()}),
            )?;
        }
    }
    let records = report["records"].as_array().unwrap();
    report["identical_outputs"] = json!(["binary_sha256", "nar_hash", "references"]
        .iter()
        .all(|f| records.iter().all(|r| r[f] == records[0][f])));
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
