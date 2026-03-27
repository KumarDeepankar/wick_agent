from setuptools import setup, find_packages

setup(
    name="opensearch-cli",
    version="0.1.0",
    packages=["opensearch_cli"],
    package_dir={"opensearch_cli": "."},
    install_requires=[
        "requests>=2.28.0",
        "click>=8.1.0",
    ],
    entry_points={
        "console_scripts": [
            "opensearch-cli=opensearch_cli.cli:cli",
        ],
    },
)
