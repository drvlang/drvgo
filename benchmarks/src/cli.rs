use std::path::PathBuf;

fn positive(arg: &str) -> Result<usize, String> {
    arg.parse::<usize>()
        .ok()
        .filter(|n| *n > 0)
        .ok_or_else(|| "expected a positive count".into())
}

#[derive(clap::Args)]
pub struct Bootstrap {
    #[arg(long)]
    pub before: PathBuf,
    #[arg(long)]
    pub after: PathBuf,
    #[arg(long)]
    pub nixpkgs: PathBuf,
    #[arg(long, value_parser = positive, default_value_t = 7)]
    pub runs: usize,
    #[arg(long, value_parser = positive, default_value_t = 2)]
    pub cores: usize,
    #[arg(long, value_parser = positive, default_value_t = 4)]
    pub jobs: usize,
    #[arg(long)]
    pub out: Option<PathBuf>,
}

#[derive(clap::Args)]
pub struct Build {
    #[arg(long)]
    pub before: PathBuf,
    #[arg(long)]
    pub after: PathBuf,
    #[arg(long, value_parser = positive, default_value_t = 5)]
    pub runs: usize,
    #[arg(long)]
    pub out: Option<PathBuf>,
}

#[derive(clap::Args)]
pub struct Ci {
    #[arg(long)]
    pub before: PathBuf,
    #[arg(long)]
    pub after: PathBuf,
    #[arg(long)]
    pub source: PathBuf,
    #[arg(long)]
    pub vendor_hash: Option<String>,
    #[arg(long)]
    pub module_cache: Option<PathBuf>,
    #[arg(long)]
    pub after_module_cache: Option<PathBuf>,
    #[arg(long)]
    pub previous_plan: Option<PathBuf>,
    #[arg(long)]
    pub after_previous_plan: Option<PathBuf>,
    #[arg(long)]
    pub before_iets: Option<PathBuf>,
    #[arg(long)]
    pub after_iets: Option<PathBuf>,
    #[arg(long)]
    pub after_prepare_modules: bool,
    #[arg(long)]
    pub after_export_data: bool,
    #[arg(long)]
    pub after_early_cutoff: bool,
    #[arg(long, default_value = ".")]
    pub package: String,
    #[arg(long, value_parser = ["0", "1"])]
    pub cgo: Option<String>,
    #[arg(long)]
    pub test: bool,
    #[arg(long)]
    pub allow_empty_tests: bool,
    #[arg(long, default_value = "")]
    pub tags: String,
    #[arg(long, default_value = "", allow_hyphen_values = true)]
    pub ldflags: String,
    #[arg(long, value_parser = positive, default_value_t = 5)]
    pub runs: usize,
    #[arg(long, num_args = 2, action = clap::ArgAction::Append, allow_hyphen_values = true)]
    pub nix_option: Vec<String>,
    #[arg(long)]
    pub out: Option<PathBuf>,
}

#[derive(clap::Args)]
pub struct Client {
    #[arg(long)]
    pub iets: PathBuf,
    #[arg(long)]
    pub source: PathBuf,
    #[arg(long, default_value = ".")]
    pub package: String,
    #[arg(long)]
    pub vendor_hash: Option<String>,
    #[arg(long)]
    pub module_cache: Option<PathBuf>,
    #[arg(long)]
    pub nested_iets: bool,
    #[arg(long, value_parser = positive, default_value_t = 5)]
    pub runs: usize,
    #[arg(long, num_args = 2, action = clap::ArgAction::Append, allow_hyphen_values = true)]
    pub nix_option: Vec<String>,
    #[arg(long)]
    pub out: Option<PathBuf>,
}

#[derive(clap::Args)]
pub struct Closure {
    #[arg(long)]
    pub before: PathBuf,
    #[arg(long)]
    pub after: PathBuf,
    #[arg(long)]
    pub out: Option<PathBuf>,
}

#[derive(clap::Args)]
pub struct Evaluation {
    #[arg(long)]
    pub before: PathBuf,
    #[arg(long)]
    pub after: PathBuf,
    #[arg(long)]
    pub iets: PathBuf,
    #[arg(long)]
    pub dynamic_source: Option<PathBuf>,
    #[arg(long, default_value = ".")]
    pub package: String,
    #[arg(long)]
    pub vendor_hash: Option<String>,
    #[arg(long)]
    pub module_cache: Option<PathBuf>,
    #[arg(long)]
    pub before_iets: Option<PathBuf>,
    #[arg(long)]
    pub after_iets: Option<PathBuf>,
    #[arg(long)]
    pub after_export_data: bool,
    #[arg(long)]
    pub after_early_cutoff: bool,
    #[arg(long, value_parser = positive, default_value_t = 7)]
    pub runs: usize,
    #[arg(long)]
    pub out: Option<PathBuf>,
}

#[derive(clap::Args)]
pub struct ExportData {
    #[arg(long)]
    pub drvgo: PathBuf,
    #[arg(long)]
    pub toolchain: PathBuf,
    #[arg(long, value_parser = positive, default_value_t = 5)]
    pub runs: usize,
    #[arg(long, value_parser = positive, default_value_t = 5)]
    pub layers: usize,
    #[arg(long)]
    pub out: Option<PathBuf>,
}

#[derive(clap::Args)]
pub struct Export {
    #[arg(long)]
    pub before: PathBuf,
    #[arg(long)]
    pub after: PathBuf,
    #[arg(long, short = 'C')]
    pub source: PathBuf,
    #[arg(long, default_value = ".")]
    pub package: String,
    #[arg(long, default_value = "")]
    pub tags: String,
    #[arg(long, default_value = "", allow_hyphen_values = true)]
    pub ldflags: String,
    #[arg(long)]
    pub module_proxy: Option<String>,
    #[arg(long, value_parser = positive, default_value_t = 3)]
    pub runs: usize,
    #[arg(long)]
    pub out: Option<PathBuf>,
}

#[derive(clap::Args)]
pub struct Pack {
    #[arg(long)]
    pub before: PathBuf,
    #[arg(long)]
    pub after: PathBuf,
    #[arg(long, value_parser = positive, default_value_t = 5)]
    pub runs: usize,
    #[arg(long, value_parser = positive, default_value_t = 100)]
    pub iterations: usize,
    #[arg(long)]
    pub out: Option<PathBuf>,
}

#[derive(clap::Args)]
pub struct PlannerClosure {
    #[arg(long)]
    pub before: String,
    #[arg(long)]
    pub after: String,
    #[arg(long)]
    pub out: Option<PathBuf>,
}

#[derive(clap::Args)]
pub struct Relocation {
    #[arg(long)]
    pub before_drvgo: PathBuf,
    #[arg(long)]
    pub before_toolchain: PathBuf,
    #[arg(long)]
    pub after_drvgo: PathBuf,
    #[arg(long)]
    pub after_toolchain: PathBuf,
    #[arg(long, value_parser = positive, default_value_t = 5)]
    pub runs: usize,
    #[arg(long)]
    pub out: Option<PathBuf>,
}

#[derive(clap::Args)]
pub struct SourceEdit {
    #[arg(long)]
    pub before: PathBuf,
    #[arg(long)]
    pub after: PathBuf,
    #[arg(long, value_parser = positive, default_value_t = 5)]
    pub runs: usize,
    #[arg(long)]
    pub out: Option<PathBuf>,
}

#[derive(clap::Parser)]
#[command(version, about = "Run the drvgo performance benchmarks")]
pub struct Cli {
    #[command(subcommand)]
    pub command: Suite,
}

#[derive(clap::Subcommand)]
pub enum Suite {
    /// Compare fresh drvgo CLI package builds, including unit tests and vet.
    Bootstrap(Bootstrap),
    /// Compare recompiling every package in two exports, with tools and inputs warm.
    Build(Build),
    /// Compare dynamic CI builds with warm package caches and fresh planning each run.
    Ci(Ci),
    /// Compare complete Nix and classic iets CI builds with the same daemon settings.
    Client(Client),
    /// Measure toolchain closures and their zstd binary-cache payloads (no network).
    Closure(Closure),
    /// Compare Nix and iets evaluation of exported graphs or dynamic helpers, without builds.
    Evaluation(Evaluation),
    /// Measure private-edit rebuilds with full archives, interfaces and early cutoff.
    ExportData(ExportData),
    /// Compare export + Nix evaluation, excluding compilation and graph inspection.
    Export(Export),
    /// Compare replaying an archive append with warm worker tools and input objects.
    Pack(Pack),
    /// Measure actual planner inputs, including recipe metadata, in a local binary cache.
    PlannerClosure(PlannerClosure),
    /// Compare cache reuse when an identical cgo checkout moves to a new directory.
    Relocation(Relocation),
    /// Compare dynamic rebuilds after application-only source edits.
    SourceEdit(SourceEdit),
}
