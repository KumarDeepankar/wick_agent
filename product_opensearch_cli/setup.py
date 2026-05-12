from setuptools import setup

setup(
    name="product-opensearch-cli",
    version="0.1.0",
    packages=["product_opensearch_cli"],
    package_dir={"product_opensearch_cli": "."},
    install_requires=[
        "requests>=2.28.0",
        "click>=8.1.0",
    ],
    entry_points={
        "console_scripts": [
            "product-opensearch-cli=product_opensearch_cli.cli:cli",
        ],
    },
)
