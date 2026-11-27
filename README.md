# Memory Safety
The rust wrapper focuses on the design, maintainability, and memory safety rather than performance. As a result, there is a spin mutex, and nothing is leaked onto the heap to have no unsafe {} usage
