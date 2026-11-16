# Packages whose upstream tests mayhem/test.sh runs (sourced by build.sh and test.sh).
# These are the packages the fuzz targets exercise: parsing, rendering and config loading.
TEST_PACKAGES="./parser/... ./markup/goldmark/... ./tpl/transform/... ./config/..."
