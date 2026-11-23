use super::*;
use crate::cli::{ExportData, Relocation, SourceEdit};
use anyhow::ensure;
pub fn source_edit(a: SourceEdit) -> Result<()> {
    let out = empty_dir(a.out.as_deref(), "drvgo-source-edit-")?;
    let source = out.join("source");
    write(
        &source,
        "go.mod",
        "module example.com/source-edit\ngo 1.25\n",
    )?;
    let roots = paths(&a.before, &a.after)?;
    let flags = flags(true);
    let nonce = nonce()?;
    let mut report = json!({"scope":"Fresh dynamic planning after each application edit; warm dependencies and no substitutions","roots":roots,"nonce":nonce,"flags":flags});
    for trial in 0..=a.runs {
        for label in alternating(&LABELS, trial) {
            let value = format!(
                "{nonce}-{}-{trial}",
                if label == "before" { "B" } else { "A" }
            );
            write(
                &source,
                "main.go",
                &format!(
                    "package main\nimport \"fmt\"\nfunc main(){{fmt.Println({})}}\n",
                    json!(value)
                ),
            )?;
            let expr = dynamic::expr(
                &roots[label],
                &source,
                ".",
                &format!(
                    "name = {};",
                    nix_string(&format!("drvgo-edit-{nonce}-{label}-{trial}"))
                ),
            )?;
            let file = out.join(format!("{label}-{trial}.log"));
            let mut row = graph_build(&expr, &flags, &fs::File::create(&file)?, false)?;
            ensure!(
                run(&args_array![
                    Path::new(row["result"].as_str().unwrap()).join("bin/source-edit")
                ])? == value,
                "program did not reflect edit"
            );
            row["label"] = json!(label);
            row["trial"] = json!(trial);
            row["warmup"] = json!(trial == 0);
            row["package_builds"] = json!(package_builds(&file)?);
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
struct GraphFixture<'a> {
    cli: &'a Path,
    toolchain: &'a Path,
    out: &'a Path,
    flags: &'a [String],
}
impl GraphFixture<'_> {
    fn build(
        &self,
        source: &Path,
        name: &str,
        options: Option<&str>,
        program: &str,
        expected: &str,
    ) -> Result<Value> {
        let graph = self.out.join(format!("{name}-export"));
        let log_path = self.out.join(format!("{name}.log"));
        let log = fs::File::create(&log_path)?;
        let cmd = argv![
            self.cli,
            "export",
            "-C",
            source,
            "-out",
            graph,
            "-toolchain",
            self.toolchain,
            "-module-proxy",
            "off",
            "."
        ];
        let start = Instant::now();
        ensure!(
            combined(&mut command(&cmd), &log)?.success(),
            "export failed: {}",
            log_path.display()
        );
        let exported = Instant::now();
        let expr = format!(
            "(import (builtins.toPath {}) {{ {} }}).default",
            quote_path(&graph)?,
            options.unwrap_or("")
        );
        let mut row = graph_build(&expr, self.flags, &log, options.is_some())?;
        row["total"] = json!(start.elapsed().as_secs_f64());
        row["export"] = json!((exported - start).as_secs_f64());
        let result = Path::new(row["result"].as_str().unwrap());
        ensure!(
            run(&args_array![result.join("bin").join(program)])? == expected,
            "incorrect built program output"
        );
        row["package_builds"] = json!(package_builds(&log_path)?);
        if options.is_none() {
            let expr = format!(
                "builtins.mapAttrs (_: d: d.drvPath) (import (builtins.toPath {}) {{}}).units",
                quote_path(&graph)?
            );
            let mapping: Value = serde_json::from_str(&run(&args_array![
                "nix-instantiate",
                "--eval",
                "--strict",
                "--json",
                "--expr",
                expr
            ])?)?;
            let manifest = read_json(&graph.join("graph.json"))?;
            let mut units = BTreeMap::new();
            for u in manifest["units"].as_array().context("units")? {
                let id = u["id"].as_str().context("unit id")?;
                units.insert(
                    u["name"].as_str().context("unit name")?,
                    mapping.get(id).context("unit absent")?,
                );
            }
            row["units"] = json!(units);
        }
        Ok(row)
    }
}
pub fn relocation(a: Relocation) -> Result<()> {
    let out = empty_dir(a.out.as_deref(), "drvgo-relocation-")?;
    let bins = paths(&a.before_drvgo, &a.after_drvgo)?;
    let tcs = paths(&a.before_toolchain, &a.after_toolchain)?;
    let flags = flags(false);
    let nonce = nonce()?;
    let mut report = json!({"scope":"Rebuild an identical cgo checkout at a new location; warm tools and dependencies, no remote lookups","flags":flags,"nonce":nonce});
    for l in LABELS {
        report["tools"][l] = json!({"drvgo":bins[l],"toolchain":tcs[l]});
    }
    for trial in 0..=a.runs {
        for l in alternating(&LABELS, trial) {
            let name = format!("{l}-{trial}");
            let module = format!("example.com/{nonce}/{l}/{trial}/relocated");
            let src = out.join(format!("{name}-first"));
            let relocated = out.join(format!("{name}-second"));
            write(&src, "go.mod", &format!("module {module}\ngo 1.25\n"))?;
            write(&src,"main.go",&format!("package main\n/*\n#cgo CFLAGS: -I.\n#include \"value.h\"\n*/\nimport \"C\"\nimport (\"fmt\"; \"{module}/native\")\nfunc main(){{fmt.Println(int(C.answer())+native.Value())}}\n"))?;
            write(&src,"native/native.go","package native\n/*\n#cgo CFLAGS: -I.\n#include \"value.h\"\n*/\nimport \"C\"\nfunc Value() int { return int(C.answer()); }\n")?;
            write(&src, "value.h", "static int answer(void) { return 20; }\n")?;
            write(
                &src,
                "native/value.h",
                "static int answer(void) { return 22; }\n",
            )?;
            copy_tree(&src, &relocated, true)?;
            let fixture = GraphFixture {
                cli: &bins[l],
                toolchain: &tcs[l],
                out: &out,
                flags: &flags,
            };
            let first = fixture.build(&src, &format!("{name}-first"), None, "relocated", "42")?;
            let second = fixture.build(
                &relocated,
                &format!("{name}-second"),
                None,
                "relocated",
                "42",
            )?;
            let changed: Vec<_> = second["units"]
                .as_object()
                .unwrap()
                .iter()
                .filter(|(k, v)| first["units"].get(k.as_str()) != Some(*v))
                .map(|(k, _)| k)
                .collect();
            record(
                &mut report,
                &out,
                json!({"label":l,"trial":trial,"warmup":trial==0,"initial":first,"relocated":second,"changed_units":changed,"reused_output":first["result"]==second["result"]}),
            )?;
        }
    }
    finish(
        &mut report,
        &out,
        &LABELS,
        "label",
        Some("relocated"),
        &["export", "evaluation", "realization", "total"],
        false,
    )
}
pub fn private_edit(a: ExportData) -> Result<()> {
    let out = empty_dir(a.out.as_deref(), "drvgo-export-data-")?;
    let cli = a.drvgo.canonicalize()?;
    let tc = a.toolchain.canonicalize()?;
    let mut flags = argv![
        "--extra-experimental-features",
        "nix-command ca-derivations"
    ];
    flags.extend(self::flags(false));
    let nonce = nonce()?;
    let modes = ["full", "interfaces"];
    let mut report = json!({"scope":"Private implementation edit; initial builds excluded from edit medians","drvgo":cli,"toolchain":tc,"flags":flags,"layers":a.layers,"nonce":nonce});
    let fixture = GraphFixture {
        cli: &cli,
        toolchain: &tc,
        out: &out,
        flags: &flags,
    };
    for trial in 0..=a.runs {
        let mut order = modes;
        order.rotate_left(trial % modes.len());
        for mode in order {
            let name = format!("{mode}-{trial}");
            let src = out.join(&name);
            let module = format!("example.com/{nonce}/{name}/privateedit");
            write(&src, "go.mod", &format!("module {module}\ngo 1.25\n"))?;
            let dep="package dep\nfunc Value() int { return privateValue() }\n//go:noinline\nfunc privateValue() int { return 42 }\n";
            write(&src, "dep/value.go", dep)?;
            let mut prev = "dep".to_owned();
            for i in 1..=a.layers {
                let pkg = format!("layer{i}");
                write(&src,&format!("{pkg}/value.go"),&format!("package {pkg}\nimport \"{module}/{prev}\"\nfunc Value() int {{ return {prev}.Value() }}\n"))?;
                prev = pkg;
            }
            write(&src,"main.go",&format!("package main\nimport (\"fmt\"; \"{module}/{prev}\")\nfunc main() {{ fmt.Println({prev}.Value()) }}\n"))?;
            let options = Some(if mode == "full" {
                "exportData = false;"
            } else {
                ""
            });
            let first = fixture.build(
                &src,
                &format!("{name}-initial"),
                options,
                "privateedit",
                "42",
            )?;
            write(&src, "dep/value.go", &dep.replace("return 42", "return 43"))?;
            let changed = fixture.build(
                &src,
                &format!("{name}-private"),
                options,
                "privateedit",
                "43",
            )?;
            let count = if mode == "interfaces" {
                2
            } else {
                a.layers + 2
            };
            ensure!(
                changed["package_builds"]
                    .as_array()
                    .context("package builds")?
                    .len()
                    == count,
                "{mode}: expected {count} package builds: {}",
                changed["package_builds"]
            );
            record(
                &mut report,
                &out,
                json!({"mode":mode,"trial":trial,"warmup":trial==0,"initial":first,"private":changed}),
            )?;
        }
    }
    finish(
        &mut report,
        &out,
        &modes,
        "mode",
        Some("private"),
        &["export", "evaluation", "realization", "total"],
        false,
    )
}
