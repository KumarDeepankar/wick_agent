from setuptools import setup

setup(
    name="launch-planner",
    version="0.1.0",
    packages=["launch_planner"],
    package_dir={"launch_planner": "."},
    install_requires=[
        "click>=8.1.0",
    ],
    entry_points={
        "console_scripts": [
            "launch-planner=launch_planner.cli:cli",
        ],
    },
)
