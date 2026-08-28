use std::num::NonZeroUsize;

#[derive(Clone, Copy, Debug, Default, Eq, PartialEq)]
pub enum FailureBehavior {
    #[default]
    Abort,
    Continue,
    Retry {
        max_retries: NonZeroUsize,
    },
}
