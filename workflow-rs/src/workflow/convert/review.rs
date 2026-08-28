use std::collections::HashSet;
use std::path::PathBuf;

use super::{BuildContext, BuildError, step_context};
use crate::workflow::raw::{RawReviewTarget, RawStep};
use crate::workflow::{
    FieldPath, OutputContract, ReviewFile, ReviewSource, ReviewStep, ReviewTarget, ReviewVerdict,
    StepId,
};

pub(crate) fn convert(
    mut raw: RawStep,
    build: &BuildContext<'_>,
) -> Result<ReviewStep, BuildError> {
    let context = step_context(&raw.id);
    super::common::reject_agent_fields(&raw, &context)?;
    if !raw.run.is_empty() || !raw.script.is_empty() {
        return Err(BuildError::Invalid {
            context,
            message: "run and script are only valid on command steps".into(),
        });
    }
    if raw.review.is_empty() {
        return Err(BuildError::Missing {
            context,
            field: "review",
        });
    }

    let targets = convert_targets(std::mem::take(&mut raw.review), &raw.id, build)?;
    let verdict =
        match super::output::scalar_contract(raw.output_type.take(), &step_context(&raw.id))? {
            OutputContract::Bool => ReviewVerdict::Bool,
            OutputContract::Enum(values) => ReviewVerdict::Enum(values),
            _ => {
                return Err(BuildError::Invalid {
                    context: step_context(&raw.id),
                    message: "review output_type must be bool or enum".into(),
                });
            }
        };
    let common = super::common::convert_common(&mut raw, false, build)?;
    Ok(ReviewStep {
        common,
        targets: targets.into_boxed_slice(),
        verdict,
    })
}

fn convert_targets(
    raw: Vec<RawReviewTarget>,
    owner: &str,
    build: &BuildContext<'_>,
) -> Result<Vec<ReviewTarget>, BuildError> {
    let mut labels = HashSet::new();
    let mut sources = HashSet::new();
    raw.into_iter()
        .map(|target| {
            let context = step_context(owner);
            let source_text = target.source.trim();
            if source_text.is_empty() {
                return Err(BuildError::Missing {
                    context,
                    field: "review.source",
                });
            }
            let label = target.label.trim().to_owned();
            if label.is_empty() {
                return Err(BuildError::Missing {
                    context,
                    field: "review.label",
                });
            }
            if !labels.insert(label.clone()) {
                return Err(BuildError::Invalid {
                    context,
                    message: format!("duplicate review label {label:?}"),
                });
            }
            if !sources.insert(source_text.to_owned()) {
                return Err(BuildError::Invalid {
                    context,
                    message: format!("duplicate review source {source_text:?}"),
                });
            }
            Ok(ReviewTarget {
                source: source(source_text, owner, build)?,
                label,
            })
        })
        .collect()
}

fn source(value: &str, owner: &str, build: &BuildContext<'_>) -> Result<ReviewSource, BuildError> {
    if value == "diff" {
        return Ok(ReviewSource::Diff);
    }
    if let Some(reference) = value.strip_prefix('@') {
        let mut segments = reference.split('.');
        let step = StepId::try_from(segments.next().unwrap_or_default()).map_err(|source| {
            BuildError::StepId {
                context: format!("{} review source", step_context(owner)),
                source,
            }
        })?;
        let fields = segments.map(str::to_owned).collect::<Vec<_>>();
        if fields
            .iter()
            .any(|field| StepId::try_from(field.as_str()).is_err())
        {
            return Err(BuildError::Invalid {
                context: step_context(owner),
                message: format!("review source {value:?} has an invalid field path"),
            });
        }
        return Ok(ReviewSource::Step {
            step,
            field: FieldPath::from_segments(fields),
        });
    }
    let authored = PathBuf::from(value);
    let resolved = build.base_dir.map(|base| base.join(&authored));
    if let Some(path) = &resolved
        && !path.is_file()
    {
        return Err(BuildError::MissingResource {
            context: step_context(owner),
            path: path.clone(),
        });
    }
    Ok(ReviewSource::File(ReviewFile::new(authored, resolved)))
}
