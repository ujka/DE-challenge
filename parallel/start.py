import sys
import subprocess

for i in range(5):
    subprocess.Popen(['time', 'go', 'run', 'Consumer.go'])
    print("started")

subprocess.Popen(['time', 'go', 'run', 'Merger.go'])
